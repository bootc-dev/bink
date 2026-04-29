package node

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/bootc-dev/bink/internal/cluster"
	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/dns"
	"github.com/bootc-dev/bink/internal/haproxy"
	"github.com/bootc-dev/bink/internal/node"
)

func newAddCmd() *cobra.Command {
	var controlPlane string
	var nodeImage string
	var role string
	var memory int

	cmd := &cobra.Command{
		Use:   "add <node-name>",
		Short: "Add a node to the cluster",
		Long:  "Create a new node (worker or control-plane) and join it to the Kubernetes cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := logrus.New()
			return runAdd(cmd.Context(), args[0], controlPlane, nodeImage, role, memory, logger)
		},
	}

	cmd.Flags().StringVarP(&controlPlane, "control-plane", "c", "node1", "Control plane node name")
	cmd.Flags().StringVar(&nodeImage, "node-image", config.DefaultNodeImage, "Container image containing base VM images")
	cmd.Flags().StringVarP(&role, "role", "r", "worker", "Node role: worker or control-plane")
	cmd.Flags().IntVar(&memory, "memory", 0, "VM memory in MB (0 = use default 8192 MB)")

	return cmd
}

func runAdd(ctx context.Context, nodeName, controlPlane, nodeImage, role string, memory int, logger *logrus.Logger) error {
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

	// Step 1: Create the new node
	logger.Infof("Step 1: Creating %s node...", role)
	logger.Infof("Node image: %s", nodeImage)

	clusterName := viper.GetString("cluster.name")

	nodeOpts := []node.NodeOption{
		node.WithNodeImage(nodeImage),
		node.WithClusterName(clusterName),
		node.WithMemory(memory),
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
	dnsMgr := dns.NewManager(dns.Config{
		ClusterName: clusterName,
		DNSServer:   controlPlane,
		Logger:      logger,
	})

	if err := dnsMgr.AddEntry(ctx, nodeName); err != nil {
		return fmt.Errorf("adding DNS entry: %w", err)
	}
	logger.Info("")

	// Step 3: Join to cluster
	logger.Info("Step 3: Joining node to cluster...")
	clusterMgr := cluster.New(cluster.Config{
		Name:         clusterName,
		ControlPlane: controlPlane,
		Logger:       logger,
	})

	if err := clusterMgr.Join(ctx, cluster.JoinOptions{
		NodeName:       nodeName,
		ControlPlane:   controlPlane,
		IsControlPlane: isControlPlane,
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
