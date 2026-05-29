// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package node

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/bootc-dev/bink/internal/cli"
	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/dns"
	"github.com/bootc-dev/bink/internal/haproxy"
	"github.com/bootc-dev/bink/internal/podman"
	"github.com/bootc-dev/bink/internal/ssh"
)

func newRemoveCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "remove <node-name>",
		Short: "Remove a node from the cluster",
		Long:  "Drain and remove a node from the Kubernetes cluster, then stop and remove its container",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: cli.CompleteNodeNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := logrus.New()
			return runRemove(cmd.Context(), args[0], force, logger)
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip draining and force remove the node")

	return cmd
}

func runRemove(ctx context.Context, nodeName string, force bool, logger *logrus.Logger) error {
	clusterName := viper.GetString("cluster.name")
	containerName := fmt.Sprintf("%s%s-%s", config.ContainerNamePrefix, clusterName, nodeName)

	logger.Infof("=== Removing node %s from cluster %s ===", nodeName, clusterName)
	logger.Info("")

	podmanClient, err := podman.NewClient()
	if err != nil {
		return fmt.Errorf("creating podman client: %w", err)
	}

	// Step 1: Verify the node container exists
	logger.Info("Step 1: Verifying node exists...")
	exists, err := podmanClient.ContainerExists(ctx, containerName)
	if err != nil {
		return fmt.Errorf("checking container: %w", err)
	}
	if !exists {
		return fmt.Errorf("node %s not found (container %s does not exist)", nodeName, containerName)
	}

	// Check if this is a control-plane node
	role, err := podmanClient.ContainerInspect(ctx, containerName, `{{index .Config.Labels "bink.node-role"}}`)
	if err != nil {
		return fmt.Errorf("inspecting node role for %s: %w", containerName, err)
	}
	isControlPlane := role == "control-plane"
	logger.Info("")

	// Step 2: Drain and delete from Kubernetes via SSH to a control-plane node
	logger.Info("Step 2: Removing node from Kubernetes...")
	cpNode, err := findControlPlaneNode(ctx, podmanClient, clusterName, nodeName)
	if err != nil {
		logger.Warnf("Could not find control-plane node: %v", err)
		logger.Warn("Node may still appear in kubectl get nodes")
	} else {
		sshClient := ssh.NewClientForNode(clusterName, cpNode, logger)
		if err := removeFromKubernetes(ctx, sshClient, nodeName, force, logger); err != nil {
			logger.Warnf("Kubernetes cleanup failed (non-fatal): %v", err)
			logger.Warn("Node may still appear in kubectl get nodes")
		}
	}
	logger.Info("")

	// Step 3: Remove DNS entry
	logger.Info("Step 3: Removing DNS entry...")
	dnsMgr, err := dns.NewManager(clusterName)
	if err != nil {
		logger.Warnf("Failed to create DNS manager: %v", err)
	} else if err := dnsMgr.RemoveEntry(ctx, nodeName); err != nil {
		logger.Warnf("Failed to remove DNS entry (non-fatal): %v", err)
	}
	logger.Info("")

	// Step 4: Stop and remove the container
	logger.Info("Step 4: Stopping and removing container...")
	if err := podmanClient.ContainerStop(ctx, containerName); err != nil {
		logger.Warnf("Failed to stop container: %v", err)
	}
	if err := podmanClient.ContainerRemove(ctx, containerName, force); err != nil {
		return fmt.Errorf("removing container %s: %w", containerName, err)
	}
	logger.Infof("Container %s removed", containerName)
	logger.Info("")

	// Step 5: Update HAProxy if this was a control-plane node
	if isControlPlane {
		logger.Info("Step 5: Updating HAProxy load balancer...")
		haproxyMgr, err := haproxy.NewManager(clusterName)
		if err != nil {
			logger.Warnf("Failed to create HAProxy manager: %v", err)
		} else if err := haproxyMgr.UpdateConfig(ctx); err != nil {
			logger.Warnf("Failed to update HAProxy (non-fatal): %v", err)
		}
		logger.Info("")
	}

	logger.Infof("Node %s removed from cluster %s", nodeName, clusterName)
	return nil
}

// findControlPlaneNode finds a control-plane node in the cluster that is not
// the node being removed.
func findControlPlaneNode(ctx context.Context, podmanClient *podman.Client, clusterName, excludeNode string) (string, error) {
	filter := fmt.Sprintf("label=bink.cluster-name=%s", clusterName)
	containers, err := podmanClient.ContainerList(ctx, filter)
	if err != nil {
		return "", fmt.Errorf("listing cluster containers: %w", err)
	}

	for _, name := range containers {
		component, _ := podmanClient.ContainerInspect(ctx, name, `{{index .Config.Labels "bink.component"}}`)
		if component != "" {
			continue
		}
		nodeName, _ := podmanClient.ContainerInspect(ctx, name, `{{index .Config.Labels "bink.node-name"}}`)
		if nodeName == excludeNode {
			continue
		}
		role, _ := podmanClient.ContainerInspect(ctx, name, `{{index .Config.Labels "bink.node-role"}}`)
		if role == "control-plane" {
			return nodeName, nil
		}
	}

	return "", fmt.Errorf("no control-plane node found in cluster %s", clusterName)
}

func removeFromKubernetes(ctx context.Context, sshClient *ssh.Client, nodeName string, force bool, logger *logrus.Logger) error {
	kubectl := "sudo kubectl --kubeconfig=/etc/kubernetes/admin.conf"

	if !force {
		logger.Infof("Draining node %s...", nodeName)
		drainCmd := fmt.Sprintf("%s drain %s --ignore-daemonsets --delete-emptydir-data --timeout=60s", kubectl, nodeName)
		if _, err := sshClient.Exec(ctx, drainCmd); err != nil {
			logger.Warnf("Failed to drain node: %v", err)
		} else {
			logger.Infof("Node %s drained", nodeName)
		}
	}

	logger.Infof("Deleting node %s from Kubernetes...", nodeName)
	deleteCmd := fmt.Sprintf("%s delete node %s", kubectl, nodeName)
	if _, err := sshClient.Exec(ctx, deleteCmd); err != nil {
		return fmt.Errorf("deleting node: %w", err)
	}
	logger.Infof("Node %s deleted from Kubernetes", nodeName)
	return nil
}
