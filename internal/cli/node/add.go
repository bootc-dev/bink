// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package node

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/bootc-dev/bink/internal/cluster"
	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/dns"
	"github.com/bootc-dev/bink/internal/haproxy"
	"github.com/bootc-dev/bink/internal/node"
	"github.com/bootc-dev/bink/internal/podman"
)

func newAddCmd() *cobra.Command {
	var nodeImage string
	var role string
	var memory int
	var maxMemory int
	var hostNetworkPopulator bool
	var labelFlags []string

	cmd := &cobra.Command{
		Use:   "add <node-name>",
		Short: "Add a node to the cluster",
		Long:  "Create a new node (worker or control-plane) and join it to the Kubernetes cluster",
		Example: `  # Add a worker node
  bink node add node2

  # Add a worker node with more memory
  bink node add node2 --memory 4096

  # Add a control-plane node
  bink node add node2 --role control-plane`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			labels, err := parseLabels(labelFlags)
			if err != nil {
				return err
			}
			logger := logrus.New()
			return runAdd(cmd.Context(), args[0], nodeImage, role, memory, maxMemory, hostNetworkPopulator, labels, logger)
		},
	}

	cmd.Flags().StringVar(&nodeImage, "node-image", config.DefaultNodeImage, "Container image containing base VM images")
	cmd.Flags().StringVarP(&role, "role", "r", "worker", "Node role: worker or control-plane")
	cmd.Flags().IntVar(&memory, "memory", 0, "VM memory in MB (0 = use role default: 1900 for control-plane, 768 for worker)")
	cmd.Flags().IntVar(&maxMemory, "max-memory", 0, "VM max memory in MB for balloon (0 = use role default: 4096 for control-plane, 2048 for worker)")
	cmd.Flags().BoolVar(&hostNetworkPopulator, "host-network-populator", false, "Use host networking for the image populator container (fixes DNS in nested podman)")
	cmd.Flags().StringArrayVarP(&labelFlags, "label", "l", nil, "Node label in key=value format (can be specified multiple times)")

	cmd.RegisterFlagCompletionFunc("role", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"worker", "control-plane"}, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

func parseLabels(labelFlags []string) (map[string]string, error) {
	labels := make(map[string]string, len(labelFlags))
	for _, l := range labelFlags {
		k, v, ok := strings.Cut(l, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid label %q: must be in key=value format", l)
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" {
			return nil, fmt.Errorf("invalid label %q: key must not be empty", l)
		}
		if _, exists := labels[k]; exists {
			return nil, fmt.Errorf("duplicate label key %q", k)
		}
		labels[k] = v
	}
	return labels, nil
}

func runAdd(ctx context.Context, nodeName, nodeImage, role string, memory int, maxMemory int, hostNetworkPopulator bool, labels map[string]string, logger *logrus.Logger) error {
	// Validate and convert role to boolean
	var isControlPlane bool
	switch role {
	case "worker":
		isControlPlane = false
	case "control-plane":
		isControlPlane = true
	default:
		return fmt.Errorf("invalid role %q: must be either 'worker' or 'control-plane'", role)
	}

	logger.Infof("=== Creating %s node %s ===", role, nodeName)
	logger.Info("")

	clusterName := viper.GetString("cluster.name")

	podmanClient, err := podman.NewClient()
	if err != nil {
		return fmt.Errorf("creating podman client: %w", err)
	}
	if err := podmanClient.EnsureImage(ctx, config.DefaultClusterImage); err != nil {
		return fmt.Errorf("ensuring cluster image: %w", err)
	}

	// Step 1: Create the new node
	logger.Infof("Step 1: Creating %s node...", role)
	logger.Infof("Node image: %s", nodeImage)

	// Collect IPs of existing nodes to avoid collisions
	existingContainers, err := podmanClient.ContainerList(ctx, config.LabelFilter(config.LabelClusterName, clusterName))
	if err != nil {
		return fmt.Errorf("listing existing nodes: %w", err)
	}
	var usedIPs []string
	for _, ctr := range existingContainers {
		ip, err := podmanClient.ContainerInspect(ctx, ctr, config.LabelInspectFormat(config.LabelClusterIP))
		if err == nil && ip != "" {
			usedIPs = append(usedIPs, ip)
		}
	}

	// Auto-detect the control-plane node name from container labels
	controlPlane, err := findControlPlaneNode(ctx, podmanClient, clusterName, "")
	if err != nil {
		return fmt.Errorf("auto-detecting control-plane node: %w", err)
	}

	// Ensure images volume exists for this node image version
	logger.Infof("Step 0: Ensuring cluster images volume...")
	clusterMgr := cluster.New(cluster.Config{
		Name:                 clusterName,
		ControlPlane:         controlPlane,
		HostNetworkPopulator: hostNetworkPopulator,
		Logger:               logger,
	})

	clusterImagesVolume, err := clusterMgr.EnsureImagesVolume(ctx, nodeImage)
	if err != nil {
		return fmt.Errorf("ensuring images volume: %w", err)
	}
	logger.Info("")

	// Discover DNS container IP for cloud-init config
	dnsMgr, err := dns.NewManager(clusterName)
	if err != nil {
		return fmt.Errorf("creating DNS manager: %w", err)
	}
	dnsIP, err := dnsMgr.EnsureContainer(ctx)
	if err != nil {
		return fmt.Errorf("ensuring DNS container: %w", err)
	}

	nodeOpts := []node.NodeOption{
		node.WithNodeImage(nodeImage),
		node.WithClusterName(clusterName),
		node.WithMemory(memory),
		node.WithMaxMemory(maxMemory),
		node.WithUsedIPs(usedIPs),
		node.WithDNSIP(dnsIP),
		node.WithClusterImagesVolume(clusterImagesVolume),
	}
	if isControlPlane {
		nodeOpts = append(nodeOpts, node.WithAPIPort(-1))
	}

	newNode, err := node.New(nodeName, isControlPlane, nodeOpts...)
	if err != nil {
		return fmt.Errorf("creating node: %w", err)
	}

	exists, err := newNode.Exists(ctx)
	if err != nil {
		return fmt.Errorf("checking if node exists: %w", err)
	}

	if exists {
		return fmt.Errorf("node %s already exists", nodeName)
	}

	if err := newNode.Create(ctx); err != nil {
		return fmt.Errorf("creating node: %w", err)
	}
	logger.Info("")

	// Step 2: Add DNS entry
	logger.Info("Step 2: Adding DNS entry...")
	if err := dnsMgr.AddEntry(ctx, nodeName); err != nil {
		return fmt.Errorf("adding DNS entry: %w", err)
	}
	logger.Info("")

	// Step 3: Join to cluster
	logger.Info("Step 3: Joining node to cluster...")

	if err := clusterMgr.Join(ctx, cluster.JoinOptions{
		NodeName:       nodeName,
		ControlPlane:   controlPlane,
		IsControlPlane: isControlPlane,
		NodeClusterIP:  newNode.ClusterIP,
		Labels:         labels,
	}); err != nil {
		return fmt.Errorf("joining node to cluster: %w", err)
	}

	// Step 4: Update HAProxy if adding a control-plane node
	if isControlPlane {
		logger.Info("")
		logger.Info("Step 4: Updating HAProxy load balancer...")
		haproxyMgr, err := haproxy.NewManager(clusterName)
		if err != nil {
			return fmt.Errorf("creating haproxy manager: %w", err)
		}
		if err := haproxyMgr.UpdateConfig(ctx); err != nil {
			logger.Warnf("Failed to update HAProxy (non-fatal): %v", err)
		}
	}

	return nil
}
