package cluster

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/network"
	"github.com/bootc-dev/bink/internal/podman"
)

func newStopCmd() *cobra.Command {
	var force bool
	var removeData bool

	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the cluster",
		Long:  "Stop and remove all cluster nodes",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := logrus.New()
			return runStop(cmd.Context(), logger, force, removeData)
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "Force stop containers")
	cmd.Flags().BoolVar(&removeData, "remove-data", false, "Remove node data (overlay disks, cloud-init ISOs, SSH keys)")

	return cmd
}

func runStop(ctx context.Context, logger *logrus.Logger, force, removeData bool) error {
	logger.Info("=== Stopping cluster ===")
	logger.Info("")

	podmanClient, err := podman.NewClient()
	if err != nil {
		return fmt.Errorf("creating podman client: %w", err)
	}

	// Find all cluster containers using label filtering
	clusterName := viper.GetString("cluster.name")
	if clusterName == "" {
		clusterName = config.DefaultNetworkName
	}

	// Use label-based filtering for more robust cluster identification
	filter := fmt.Sprintf("label=bink.cluster-name=%s", clusterName)
	containers, err := podmanClient.ContainerList(ctx, filter)
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}

	if clusterName == config.DefaultNetworkName {
		logger.Info("Stopping default cluster")
	} else {
		logger.Infof("Stopping cluster: %s", clusterName)
	}

	if len(containers) == 0 {
		logger.Info("No cluster nodes found")
		return nil
	}

	logger.Infof("Found %d node(s) to stop:", len(containers))
	for _, container := range containers {
		logger.Infof("  - %s", container)
	}
	logger.Info("")

	// Stop and remove each container
	for _, container := range containers {
		if container == "" {
			continue
		}

		logger.Infof("Stopping container: %s", container)
		if err := podmanClient.ContainerStop(ctx, container); err != nil {
			logger.Warnf("Failed to stop %s: %v", container, err)
		}

		logger.Infof("Removing container: %s", container)
		if err := podmanClient.ContainerRemove(ctx, container, force); err != nil {
			logger.Warnf("Failed to remove %s: %v", container, err)
		}
	}

	logger.Info("")
	logger.Info("✅ All cluster nodes stopped and removed")

	if removeData {
		logger.Info("")
		logger.Info("Removing cluster data...")

		if err := removeClusterData(logger, clusterName, containers); err != nil {
			logger.Warnf("Failed to remove some data: %v", err)
			logger.Warn("You may need to manually clean up:")
			clusterKeysVolume := fmt.Sprintf("%s-cluster-keys", clusterName)
			logger.Warnf("  - Cluster keys volume: podman volume rm %s", clusterKeysVolume)
			logger.Warn("  - Kubeconfig: rm -f ./vm/kubeconfig")
		} else {
			logger.Info("✅ All cluster data removed")
		}
		logger.Info("Note: Shared registry (bink-registry) is preserved. Use 'bink registry stop' to remove it.")
	}

	return nil
}

func removeClusterData(logger *logrus.Logger, clusterName string, containers []string) error {
	var errors []string

	podmanClient, err := podman.NewClient()
	if err != nil {
		return fmt.Errorf("creating podman client: %w", err)
	}
	ctx := context.Background()

	clusterKeysVolume := fmt.Sprintf("%s-cluster-keys", clusterName)

	// Remove cluster-keys volume
	logger.Infof("Removing cluster-keys volume: %s...", clusterKeysVolume)
	if err := podmanClient.VolumeRemove(ctx, clusterKeysVolume); err != nil {
		logger.Warnf("Failed to remove cluster-keys volume: %v", err)
		errors = append(errors, err.Error())
	} else {
		logger.Infof("Removed cluster-keys volume: %s", clusterKeysVolume)
	}

	// Remove cluster-specific kubeconfig if it exists
	kubeconfigPath := filepath.Join(config.DefaultKubeconfigDir, fmt.Sprintf("kubeconfig-%s", clusterName))
	if err := os.Remove(kubeconfigPath); err != nil {
		if !os.IsNotExist(err) {
			logger.Warnf("Failed to remove kubeconfig %s: %v", kubeconfigPath, err)
			errors = append(errors, err.Error())
		}
	} else {
		logger.Infof("Removed kubeconfig: %s", kubeconfigPath)
	}

	logger.Info("Note: Overlay disks and cloud-init ISOs are stored in ephemeral container storage and removed automatically")

	// Remove cluster-specific network
	if clusterName != "" && clusterName != config.DefaultNetworkName {
		logger.Infof("Removing cluster network: %s...", clusterName)
		netMgr, err := network.NewManager()
		if err != nil {
			logger.Warnf("Failed to create network manager: %v", err)
			errors = append(errors, err.Error())
		} else {
			if err := netMgr.Remove(ctx, clusterName); err != nil {
				logger.Warnf("Failed to remove network: %v", err)
				errors = append(errors, err.Error())
			} else {
				logger.Infof("Removed network: %s", clusterName)
			}
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("encountered %d error(s) during cleanup", len(errors))
	}

	return nil
}

// hasPrefix checks if a string starts with the given prefix
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// isDefaultClusterContainer checks if a container name belongs to the default cluster
// Default cluster containers have the format: k8s-<node> (e.g., k8s-node1)
// Named cluster containers have the format: k8s-<cluster>-<node> (e.g., k8s-mycluster-node1)
func isDefaultClusterContainer(name string) bool {
	if !hasPrefix(name, config.ContainerNamePrefix) {
		return false
	}

	// Remove the k8s- prefix
	remainder := name[len(config.ContainerNamePrefix):]

	// If the remainder contains a hyphen, it's a named cluster container
	// (e.g., "mycluster-node1" has a hyphen)
	// Default cluster containers have no hyphen (e.g., "node1")
	for i := 0; i < len(remainder); i++ {
		if remainder[i] == '-' {
			return false
		}
	}

	return true
}
