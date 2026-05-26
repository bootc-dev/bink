// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"context"
	"fmt"

	"github.com/bootc-dev/bink/internal/cli/api"
	"github.com/bootc-dev/bink/internal/cluster"
	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/dns"
	"github.com/bootc-dev/bink/internal/haproxy"
	"github.com/bootc-dev/bink/internal/network"
	"github.com/bootc-dev/bink/internal/node"
	"github.com/bootc-dev/bink/internal/podman"
	"github.com/bootc-dev/bink/internal/registry"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newStartCmd() *cobra.Command {
	var nodeName string
	var nodeImage string
	var apiPort int
	var memory int
	var maxMemory int
	var exposePath string

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start a new Kubernetes cluster",
		Long:  "Create network, control plane node, and initialize Kubernetes cluster with kubeadm",
		Example: `  # Start a cluster with default settings
  bink cluster start

  # Start a named cluster with auto-assigned API port
  bink cluster start --cluster-name dev --api-port 0

  # Start a cluster with more memory and auto-expose the API
  bink cluster start --memory 4096 --expose ./kubeconfig`,
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := logrus.New()
			return runStart(cmd.Context(), logger, nodeName, nodeImage, apiPort, memory, maxMemory, exposePath)
		},
	}

	cmd.Flags().StringVar(&nodeName, "node-name", "node1", "Name for the control plane node")
	cmd.Flags().StringVar(&nodeImage, "node-image", config.DefaultNodeImage, "Container image containing base VM images")
	cmd.Flags().IntVar(&apiPort, "api-port", 0, "API server port to expose (0 = auto-assign random port)")
	cmd.Flags().IntVar(&memory, "memory", 0, "VM memory in MB (0 = use role default: 1900 for control-plane, 768 for worker)")
	cmd.Flags().IntVar(&maxMemory, "max-memory", 0, "VM max memory in MB for balloon (0 = use role default: 4096 for control-plane, 2048 for worker)")
	cmd.Flags().StringVar(&exposePath, "expose", "", "Expose API and save kubeconfig to PATH after cluster is up")

	return cmd
}

func runStart(ctx context.Context, logger *logrus.Logger, nodeName string, nodeImage string, apiPort int, memory int, maxMemory int, exposePath string) error {
	logger.Info("=== Creating Kubernetes cluster ===")
	logger.Info("")

	clusterName := viper.GetString("cluster.name")

	logger.Info("Step 1: Creating cluster network...")
	netMgr, err := network.NewManager()
	if err != nil {
		return fmt.Errorf("creating network manager: %w", err)
	}
	if err := netMgr.EnsureClusterNetwork(ctx, clusterName); err != nil {
		return fmt.Errorf("ensuring cluster network: %w", err)
	}
	logger.Info("")

	logger.Info("Step 2: Ensuring local registry...")
	registryMgr, err := registry.NewManager()
	if err != nil {
		return fmt.Errorf("creating registry manager: %w", err)
	}
	if err := registryMgr.EnsureRegistry(ctx); err != nil {
		return fmt.Errorf("ensuring registry: %w", err)
	}
	logger.Info("")

	logger.Info("Step 3: Ensuring DNS container...")
	dnsMgr, err := dns.NewManager(clusterName)
	if err != nil {
		return fmt.Errorf("creating DNS manager: %w", err)
	}
	dnsIP, err := dnsMgr.EnsureContainer(ctx)
	if err != nil {
		return fmt.Errorf("ensuring DNS container: %w", err)
	}
	logger.Infof("DNS server running at %s:53", dnsIP)
	logger.Info("")

	logger.Info("Step 4: Ensuring required images...")
	podmanClient, err := podman.NewClient()
	if err != nil {
		return fmt.Errorf("creating podman client: %w", err)
	}
	if err := podmanClient.EnsureImage(ctx, config.DefaultClusterImage); err != nil {
		return fmt.Errorf("ensuring cluster image: %w", err)
	}
	if err := podmanClient.EnsureImage(ctx, nodeImage); err != nil {
		return fmt.Errorf("ensuring node image: %w", err)
	}
	logger.Info("")

	logger.Info("Step 5: Preparing cluster images volume...")
	clusterMgr := cluster.New(cluster.Config{
		Name:         clusterName,
		ControlPlane: nodeName,
		Logger:       logger,
	})

	clusterImagesVolume, err := clusterMgr.EnsureImagesVolume(ctx, nodeImage)
	if err != nil {
		return fmt.Errorf("ensuring images volume: %w", err)
	}
	logger.Info("")

	logger.Infof("Step 6: Creating control plane node (%s)...", nodeName)
	logger.Infof("Node image: %s", nodeImage)

	// Convert 0 to -1 for auto-assign (to distinguish from unset)
	if apiPort == 0 {
		apiPort = -1
	}

	controlPlane, err := node.New(nodeName, true,
		node.WithNodeImage(nodeImage),
		node.WithClusterName(clusterName),
		node.WithAPIPort(apiPort),
		node.WithMemory(memory),
		node.WithMaxMemory(maxMemory),
		node.WithDNSIP(dnsIP),
		node.WithClusterImagesVolume(clusterImagesVolume),
	)
	if err != nil {
		return fmt.Errorf("creating node: %w", err)
	}

	exists, err := controlPlane.Exists(ctx)
	if err != nil {
		return fmt.Errorf("checking if node exists: %w", err)
	}

	if exists {
		return fmt.Errorf("%s already exists. Run 'bink cluster stop' first", nodeName)
	}

	if err := controlPlane.Create(ctx); err != nil {
		return fmt.Errorf("creating control plane node: %w", err)
	}

	logger.Infof("Adding %s DNS entry...", nodeName)
	if err := dnsMgr.AddEntry(ctx, nodeName); err != nil {
		return fmt.Errorf("adding %s DNS entry: %w", nodeName, err)
	}
	logger.Info("")

	logger.Info("Step 7: Initializing Kubernetes cluster...")

	if err := clusterMgr.Init(ctx, cluster.InitOptions{
		NodeName: nodeName,
	}); err != nil {
		return fmt.Errorf("initializing cluster: %w", err)
	}

	logger.Info("")

	logger.Info("Step 8: Creating HAProxy load balancer...")
	haproxyMgr, err := haproxy.NewManager(clusterName)
	if err != nil {
		return fmt.Errorf("creating haproxy manager: %w", err)
	}
	// Use auto-assigned port (0) for HAProxy — the user-specified apiPort is for the node
	if err := haproxyMgr.EnsureHAProxy(ctx, 0); err != nil {
		return fmt.Errorf("creating HAProxy load balancer: %w", err)
	}
	logger.Info("")

	logger.Info("✅ Cluster created successfully!")
	logger.Info("")
	logger.Info("Local registry:")
	logger.Infof("  Push:  podman push --tls-verify=false localhost:%d/<image>:<tag>", config.RegistryPort)
	logger.Infof("  Pull (in-cluster): %s.%s:%d/<image>:<tag>", config.RegistryHostname, config.ClusterDomain, config.RegistryPort)
	logger.Info("")

	if exposePath != "" {
		logger.Info("Step 9: Exposing API server...")
		if err := api.RunExpose(ctx, logger, "", exposePath); err != nil {
			return fmt.Errorf("exposing API: %w", err)
		}
	} else {
		logger.Info("Next steps:")
		logger.Info("  ./bink api expose")
		logger.Info("")
		logger.Info("Then use:")
		logger.Info("  export KUBECONFIG=./kubeconfig")
		logger.Info("  kubectl get nodes")
	}
	logger.Info("")
	logger.Info("To add worker nodes:")
	logger.Info("  bink node add node2")

	return nil
}
