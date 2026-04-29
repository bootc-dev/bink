package cluster

import (
	"context"
	"fmt"

	"github.com/bootc-dev/bink/internal/cluster"
	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/haproxy"
	"github.com/bootc-dev/bink/internal/network"
	"github.com/bootc-dev/bink/internal/node"
	"github.com/bootc-dev/bink/internal/registry"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newStartCmd() *cobra.Command {
	var nodeImage string
	var apiPort int
	var memory int

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start a new Kubernetes cluster",
		Long:  "Create network, control plane node, and initialize Kubernetes cluster with kubeadm",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := logrus.New()
			return runStart(cmd.Context(), logger, nodeImage, apiPort, memory)
		},
	}

	cmd.Flags().StringVar(&nodeImage, "node-image", config.DefaultNodeImage, "Container image containing base VM images")
	cmd.Flags().IntVar(&apiPort, "api-port", 0, "API server port to expose (0 = auto-assign random port)")
	cmd.Flags().IntVar(&memory, "memory", 0, "VM memory in MB (0 = use default 8192 MB)")

	return cmd
}

func runStart(ctx context.Context, logger *logrus.Logger, nodeImage string, apiPort int, memory int) error {
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

	logger.Info("Step 3: Preparing cluster images volume...")
	clusterMgr := cluster.New(cluster.Config{
		Name:         clusterName,
		ControlPlane: "node1",
		Logger:       logger,
	})

	if err := clusterMgr.EnsureImagesVolume(ctx); err != nil {
		return fmt.Errorf("ensuring images volume: %w", err)
	}
	logger.Info("")

	logger.Info("Step 4: Creating control plane node (node1)...")
	logger.Infof("Node image: %s", nodeImage)

	// Convert 0 to -1 for auto-assign (to distinguish from unset)
	if apiPort == 0 {
		apiPort = -1
	}

	controlPlane, err := node.New("node1", true,
		node.WithNodeImage(nodeImage),
		node.WithClusterName(clusterName),
		node.WithAPIPort(apiPort),
		node.WithMemory(memory),
	)
	if err != nil {
		return fmt.Errorf("creating node: %w", err)
	}

	exists, err := controlPlane.Exists(ctx)
	if err != nil {
		return fmt.Errorf("checking if node exists: %w", err)
	}

	if exists {
		return fmt.Errorf("node1 already exists. Run 'bink cluster stop' first")
	}

	if err := controlPlane.Create(ctx); err != nil {
		return fmt.Errorf("creating control plane node: %w", err)
	}
	logger.Info("")

	logger.Info("Step 5: Initializing Kubernetes cluster...")

	if err := clusterMgr.Init(ctx, cluster.InitOptions{
		NodeName: "node1",
	}); err != nil {
		return fmt.Errorf("initializing cluster: %w", err)
	}

	logger.Info("")

	logger.Info("Step 6: Creating HAProxy load balancer...")
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
	logger.Info("Next steps:")
	logger.Info("  ./bink api expose")
	logger.Info("")
	logger.Info("Then use:")
	logger.Info("  export KUBECONFIG=./kubeconfig")
	logger.Info("  kubectl get nodes")
	logger.Info("")
	logger.Info("To add worker nodes:")
	logger.Info("  bink node add node2")

	return nil
}
