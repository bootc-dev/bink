package cluster

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/podman"
)

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all clusters",
		Long:  "List all running clusters and their node counts",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := logrus.New()
			return runClusterList(cmd.Context(), logger)
		},
	}

	return cmd
}

type clusterInfo struct {
	name         string
	nodes        []string
	runningCount int
	stoppedCount int
}

func runClusterList(ctx context.Context, logger *logrus.Logger) error {
	podmanClient, err := podman.NewClient()
	if err != nil {
		return fmt.Errorf("creating podman client: %w", err)
	}

	filter := fmt.Sprintf("name=%s", config.ContainerNamePrefix)
	containers, err := podmanClient.ContainerList(ctx, filter)
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}

	if len(containers) == 0 {
		fmt.Println("No clusters found")
		return nil
	}

	// Group containers by cluster
	clusters := make(map[string]*clusterInfo)

	for _, containerName := range containers {
		if containerName == "" {
			continue
		}

		// Get cluster name from label
		clusterNameLabel, err := podmanClient.ContainerInspect(ctx, containerName, "{{index .Config.Labels \"bink.cluster-name\"}}")
		if err != nil {
			logger.Warnf("Failed to get cluster name for %s: %v", containerName, err)
			continue
		}

		clusterNameLabel = strings.TrimSpace(clusterNameLabel)
		if clusterNameLabel == "" {
			clusterNameLabel = "podman"
		}

		// Get node name from label
		nodeName, err := podmanClient.ContainerInspect(ctx, containerName, "{{index .Config.Labels \"bink.node-name\"}}")
		if err != nil {
			logger.Warnf("Failed to get node name for %s: %v", containerName, err)
			continue
		}
		nodeName = strings.TrimSpace(nodeName)
		if nodeName == "" {
			continue
		}

		// Get container state
		state, err := podmanClient.ContainerInspect(ctx, containerName, "{{.State.Status}}")
		if err != nil {
			logger.Warnf("Failed to get state for %s: %v", containerName, err)
			state = "unknown"
		}
		state = strings.TrimSpace(state)

		// Initialize cluster info if not exists
		if _, exists := clusters[clusterNameLabel]; !exists {
			clusters[clusterNameLabel] = &clusterInfo{
				name:  clusterNameLabel,
				nodes: []string{},
			}
		}

		cluster := clusters[clusterNameLabel]
		cluster.nodes = append(cluster.nodes, nodeName)

		if state == "running" {
			cluster.runningCount++
		} else {
			cluster.stoppedCount++
		}
	}

	// Display clusters
	fmt.Printf("Found %d cluster(s):\n\n", len(clusters))

	for _, cluster := range clusters {
		totalNodes := len(cluster.nodes)
		statusSymbol := ""
		var statusText string

		switch {
		case cluster.runningCount == totalNodes:
			statusSymbol = "✓"
			statusText = "running"
		case cluster.runningCount > 0:
			statusSymbol = "⚠"
			statusText = fmt.Sprintf("partially running (%d/%d)", cluster.runningCount, totalNodes)
		default:
			statusSymbol = "✗"
			statusText = "stopped"
		}

		fmt.Printf("  %s %s (%d node(s), %s)\n", statusSymbol, cluster.name, totalNodes, statusText)
	}

	fmt.Println()

	return nil
}
