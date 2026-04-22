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

// Cluster represents a Kubernetes cluster
type Cluster struct {
	name         string
	controlPlane string
	logger       *logrus.Logger
	podmanClient *podman.Client
}

// Config holds cluster configuration
type Config struct {
	Name         string // Cluster name (default: "bink")
	ControlPlane string // Control plane node name (default: "node1")
	Logger       *logrus.Logger
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

	podmanClient, err := podman.NewClient()
	if err != nil {
		cfg.Logger.Warnf("Failed to create podman client: %v", err)
	}

	return &Cluster{
		name:         cfg.Name,
		controlPlane: cfg.ControlPlane,
		logger:       cfg.Logger,
		podmanClient: podmanClient,
	}
}

// GetControlPlane returns the control plane node name
func (c *Cluster) GetControlPlane() string {
	return c.controlPlane
}

// GetName returns the cluster name
func (c *Cluster) GetName() string {
	return c.name
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

		// Accept "done" (done with or without warnings is OK)
		if status == "done" {
			c.logger.Info("✓ cloud-init completed")
			return nil
		}

		if i == maxRetries {
			// Get full status for debugging
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
	return node.CalculateClusterIP(nodeName)
}
