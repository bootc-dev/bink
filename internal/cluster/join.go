// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/ssh"
)

// JoinOptions holds options for joining a node to the cluster
type JoinOptions struct {
	NodeName       string
	ControlPlane   string
	IsControlPlane bool
	NodeClusterIP  string
	Timeout        time.Duration
	Labels         map[string]string
}

// Join joins a node to the cluster
func (c *Cluster) Join(ctx context.Context, opts JoinOptions) error {
	if opts.Timeout == 0 {
		opts.Timeout = 10 * time.Minute
	}

	if opts.ControlPlane == "" {
		opts.ControlPlane = c.controlPlane
	}

	nodeName := opts.NodeName
	controlPlane := opts.ControlPlane

	nodeType := "worker"
	if opts.IsControlPlane {
		nodeType = "control-plane"
	}

	c.logger.Info("")
	c.logger.Infof("=== Generating %s join command from %s ===", nodeType, controlPlane)

	// Create SSH client for control plane
	cpSSHClient := ssh.NewClientForNode(c.name, controlPlane, c.logger)

	// Generate join command
	joinCommand, err := c.generateJoinCommand(ctx, cpSSHClient, opts.IsControlPlane)
	if err != nil {
		return fmt.Errorf("failed to generate join command: %w", err)
	}

	c.logger.Infof("Join command: %s", joinCommand)

	c.logger.Info("")
	c.logger.Infof("=== Waiting for %s to be ready ===", nodeName)

	// Wait for cloud-init on the new node
	if err := c.WaitForCloudInit(ctx, nodeName, opts.Timeout); err != nil {
		return err
	}

	c.logger.Info("")
	c.logger.Infof("=== Joining %s to the cluster ===", nodeName)

	// Create SSH client for the new node
	nodeSSHClient := ssh.NewClientForNode(c.name, nodeName, c.logger)

	// For control-plane joins, set the advertise address to the node's cluster IP
	// so the API server binds to the cluster network interface
	if opts.IsControlPlane && opts.NodeClusterIP != "" {
		joinCommand = fmt.Sprintf("%s --apiserver-advertise-address %s", joinCommand, opts.NodeClusterIP)
	}

	// Execute join command
	if err := nodeSSHClient.ExecWithOutput(ctx, fmt.Sprintf("sudo %s", joinCommand)); err != nil {
		return fmt.Errorf("failed to join node: %w", err)
	}

	// Set up kubectl for control-plane nodes
	if opts.IsControlPlane {
		c.logger.Info("")
		c.logger.Infof("=== Setting up kubectl on %s ===", nodeName)
		if _, err := nodeSSHClient.Exec(ctx, "mkdir -p $HOME/.kube && sudo cp -i /etc/kubernetes/admin.conf $HOME/.kube/config && sudo chown $(id -u):$(id -g) $HOME/.kube/config"); err != nil {
			c.logger.Warnf("Failed to setup kubectl on %s (non-fatal): %v", nodeName, err)
		}
	}

	// Label nodes (role label applied last to prevent user override)
	labels := make(map[string]string)
	for k, v := range opts.Labels {
		labels[k] = v
	}
	if !opts.IsControlPlane {
		labels["node-role.kubernetes.io/worker"] = ""
	}

	if len(labels) > 0 {
		c.logger.Info("")
		c.logger.Infof("=== Labeling %s ===", nodeName)

		haproxyContainer := fmt.Sprintf("%s%s-%s", config.ContainerNamePrefix, c.name, config.HAProxyContainerName)
		kubeClient, err := c.newKubeClient(ctx, cpSSHClient, haproxyContainer)
		if err != nil {
			return fmt.Errorf("labeling node %s: creating kubernetes client: %w", nodeName, err)
		}
		var labelErr error
		for attempt := 1; attempt <= 5; attempt++ {
			labelErr = kubeClient.LabelNode(ctx, nodeName, labels)
			if labelErr == nil {
				break
			}
			c.logger.Warnf("Failed to label node (attempt %d/5): %v", attempt, labelErr)
			time.Sleep(5 * time.Second)
		}
		if labelErr != nil {
			return fmt.Errorf("labeling node %s: %w", nodeName, labelErr)
		}
		c.logger.Infof("✅ Node %s labeled", nodeName)
	}

	c.logger.Info("")
	c.logger.Infof("✅ Node %s successfully joined the cluster!", nodeName)
	c.logger.Info("")
	c.logger.Info("Verify with:")
	c.logger.Infof("  bink node ssh %s", controlPlane)
	c.logger.Info("  kubectl get nodes")

	return nil
}

// generateJoinCommand generates a fresh join command from the control plane
func (c *Cluster) generateJoinCommand(ctx context.Context, cpSSHClient *ssh.Client, isControlPlane bool) (string, error) {
	if isControlPlane {
		// Generate a certificate key, then upload certs encrypted with that key
		c.logger.Info("Uploading certificates for control-plane join...")
		certKeyOutput, err := cpSSHClient.Exec(ctx, "sudo kubeadm certs certificate-key")
		if err != nil {
			return "", fmt.Errorf("failed to generate certificate key: %w", err)
		}
		certificateKey := strings.TrimSpace(certKeyOutput)
		if certificateKey == "" {
			return "", fmt.Errorf("certificate key is empty")
		}

		if _, err := cpSSHClient.Exec(ctx, fmt.Sprintf("sudo kubeadm init phase upload-certs --upload-certs --certificate-key %s", certificateKey)); err != nil {
			return "", fmt.Errorf("failed to upload certificates: %w", err)
		}

		// Generate join command with control-plane flag
		output, err := cpSSHClient.Exec(ctx, "sudo kubeadm token create --print-join-command")
		if err != nil {
			return "", fmt.Errorf("failed to generate join command: %w", err)
		}

		baseCommand := strings.TrimSpace(output)
		if baseCommand == "" {
			return "", fmt.Errorf("join command is empty")
		}

		// Add control-plane flag and certificate key
		joinCommand := fmt.Sprintf("%s --control-plane --certificate-key %s", baseCommand, certificateKey)
		return joinCommand, nil
	}

	// For worker nodes, just generate a standard join command
	output, err := cpSSHClient.Exec(ctx, "sudo kubeadm token create --print-join-command")
	if err != nil {
		return "", fmt.Errorf("failed to generate join command: %w", err)
	}

	// Trim whitespace
	joinCommand := strings.TrimSpace(output)

	if joinCommand == "" {
		return "", fmt.Errorf("join command is empty")
	}

	return joinCommand, nil
}
