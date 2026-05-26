// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package node

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/bootc-dev/bink/internal/node"
	"github.com/bootc-dev/bink/internal/ssh"
)

func newSSHCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ssh <node-name>",
		Short: "SSH into a node's VM",
		Long:  "Start an interactive SSH session to a node's VM",
		Example: `  # SSH into the control-plane node
  bink node ssh node1

  # SSH into a worker node in a named cluster
  bink node ssh node2 --cluster-name dev`,
		Args: cobra.ExactArgs(1),
		RunE: runSSH,
	}

	return cmd
}

func runSSH(cmd *cobra.Command, args []string) error {
	nodeName := args[0]

	ctx := context.Background()
	logger := logrus.New()

	// Get cluster name
	clusterName := viper.GetString("cluster.name")

	// Get node IP for display
	clusterIP := node.CalculateClusterIP(clusterName, nodeName)

	// Create SSH client
	sshClient := ssh.NewClientForNode(clusterName, nodeName, logger)

	fmt.Printf("Connecting to %s (SSH: %s:%s, cluster: %s) as user core\n",
		nodeName, ssh.DefaultSSHHost, ssh.DefaultSSHPort, clusterIP)

	// Start interactive session
	if err := sshClient.Interactive(ctx); err != nil {
		return fmt.Errorf("failed to connect to %s: %w", nodeName, err)
	}

	return nil
}
