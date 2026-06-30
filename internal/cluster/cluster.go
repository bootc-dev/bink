// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/bootc-dev/bink/internal/node"
	"github.com/bootc-dev/bink/internal/podman"
	"github.com/bootc-dev/bink/internal/ssh"
)

type PodmanClient interface {
	EnsureImage(ctx context.Context, image string) error
	ImageInspectLabels(ctx context.Context, name string) (map[string]string, error)
	VolumeExists(ctx context.Context, name string) (bool, error)
	VolumeCreate(ctx context.Context, name string, labels map[string]string) error
	ContainerCreate(ctx context.Context, opts *podman.ContainerCreateOptions) (string, error)
	ContainerExists(ctx context.Context, name string) (bool, error)
	ContainerRemove(ctx context.Context, name string, force bool) error
	ContainerCopyContent(ctx context.Context, content []byte, containerName, destPath string, mode int64) error
	ContainerExec(ctx context.Context, name string, cmd []string) (string, error)
	ContainerExecQuiet(ctx context.Context, name string, cmd []string) error
	ContainerRunQuiet(ctx context.Context, image string, cmd []string, volumeMounts []string) error
	ContainerWait(ctx context.Context, name string) (int64, error)
	GetPublishedPort(ctx context.Context, containerName, containerPort string) (int, error)
}

// Cluster represents a Kubernetes cluster
type Cluster struct {
	name                 string
	controlPlane         string
	hostNetworkPopulator bool
	logger               *logrus.Logger
	podmanClient         PodmanClient
}

// Config holds cluster configuration
type Config struct {
	Name                 string // Cluster name (default: "bink")
	ControlPlane         string // Control plane node name (default: "node1")
	HostNetworkPopulator bool   // Use host networking for the image populator container
	Logger               *logrus.Logger
	PodmanClient         PodmanClient
}

// New creates a new Cluster
func New(cfg Config) *Cluster {
	if cfg.Logger == nil {
		cfg.Logger = logrus.New()
	}
	if cfg.Name == "" {
		cfg.Name = "bink"
	}
	if cfg.ControlPlane == "" {
		cfg.ControlPlane = "node1"
	}

	var client PodmanClient = cfg.PodmanClient
	if client == nil {
		c, err := podman.NewClient()
		if err != nil {
			cfg.Logger.Warnf("Failed to create podman client: %v", err)
		}
		client = c
	}

	return &Cluster{
		name:                 cfg.Name,
		controlPlane:         cfg.ControlPlane,
		hostNetworkPopulator: cfg.HostNetworkPopulator,
		logger:               cfg.Logger,
		podmanClient:         client,
	}
}

// WaitForCloudInit waits for cloud-init to complete on a node
func (c *Cluster) WaitForCloudInit(ctx context.Context, nodeName string, timeout time.Duration) error {
	c.logger.Infof("Waiting for cloud-init to complete on %s...", nodeName)

	sshClient := ssh.NewClientForNode(c.name, nodeName, c.logger)

	// First wait for SSH to be ready
	if err := sshClient.WaitForSSH(ctx, 30); err != nil {
		return fmt.Errorf("SSH not ready on %s: %w", nodeName, err)
	}

	// Then wait for cloud-init to complete
	c.logger.Info("Checking cloud-init status...")
	maxRetries := int(timeout / (5 * time.Second))
	for i := 1; i <= maxRetries; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		output, err := sshClient.Exec(ctx, "cloud-init status 2>/dev/null | head -1 | awk '{print $2}'")
		if err != nil {
			c.logger.Infof("cloud-init status check failed (attempt %d/%d): %v", i, maxRetries, err)
			time.Sleep(5 * time.Second)
			continue
		}

		status := output
		if len(status) > 0 && status[len(status)-1] == '\n' {
			status = status[:len(status)-1]
		}

		c.logger.Infof("cloud-init status: %s (attempt %d/%d)", status, i, maxRetries)

		switch status {
		case "done":
			c.logger.Info("✓ cloud-init completed")
			return nil
		case "error":
			c.logger.Warn("cloud-init finished with errors (non-critical modules may have failed)")
			fullStatus, _ := sshClient.Exec(ctx, "cloud-init status --long")
			c.logger.Debugf("cloud-init full status:\n%s", fullStatus)
			return nil
		}

		if i == maxRetries {
			fullStatus, _ := sshClient.Exec(ctx, "cloud-init status --long")
			return fmt.Errorf("timeout waiting for cloud-init to complete on %s. Status: %s\nFull status:\n%s",
				nodeName, status, fullStatus)
		}

		c.logger.Debug(".")
		time.Sleep(5 * time.Second)
	}

	return nil
}

// GetNodeClusterIP returns the cluster IP for a node
func (c *Cluster) GetNodeClusterIP(nodeName string) string {
	return node.CalculateClusterIP(c.name, nodeName)
}
