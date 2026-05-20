// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package node

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
	var showAll bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List cluster nodes",
		Long:  "List all cluster nodes (containers with k8s- prefix) and their status",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := logrus.New()
			return runList(cmd.Context(), logger, showAll)
		},
	}

	cmd.Flags().BoolVarP(&showAll, "all", "a", false, "Show all containers (including stopped)")

	return cmd
}

func runList(ctx context.Context, logger *logrus.Logger, showAll bool) error {
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
		fmt.Println("No cluster nodes found")
		return nil
	}

	fmt.Printf("Found %d cluster node(s):\n\n", len(containers))

	for _, containerName := range containers {
		if containerName == "" {
			continue
		}

		nodeName := strings.TrimPrefix(containerName, config.ContainerNamePrefix)

		state, err := podmanClient.ContainerInspect(ctx, containerName, "{{.State.Status}}")
		if err != nil {
			logger.Warnf("Failed to inspect %s: %v", containerName, err)
			fmt.Printf("  %s (status unknown)\n", nodeName)
			continue
		}

		state = strings.TrimSpace(state)

		created, err := podmanClient.ContainerInspect(ctx, containerName, "{{.Created}}")
		if err == nil {
			created = strings.TrimSpace(created)
			if len(created) > 19 {
				created = created[:19]
			}
		} else {
			created = "unknown"
		}

		statusSymbol := ""
		switch state {
		case "running":
			statusSymbol = "✓"
		case "exited":
			statusSymbol = "✗"
		case "paused":
			statusSymbol = "⏸"
		default:
			statusSymbol = "?"
		}

		fmt.Printf("  %s %s (status: %s, created: %s)\n", statusSymbol, nodeName, state, created)
	}

	fmt.Println()

	return nil
}
