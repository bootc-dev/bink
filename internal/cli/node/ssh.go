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

	"github.com/bootc-dev/bink/internal/cli"
	"github.com/bootc-dev/bink/internal/node"
	"github.com/bootc-dev/bink/internal/ssh"
)

func newSSHCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ssh <node-name> [-- command [args...]]",
		Short: "SSH into a node's VM",
		Long:  "Start an interactive SSH session to a node's VM, or run a command and exit.",
		Example: `  # SSH into the control-plane node
  bink node ssh node1

  # SSH into a worker node in a named cluster
  bink node ssh node2 --cluster-name dev

  # Run a command on a node
  bink node ssh node1 -- uname -a

  # Run kubectl on the control-plane node
  bink node ssh node1 -- sudo kubectl --kubeconfig=/etc/kubernetes/admin.conf get nodes`,
		Args: func(cmd *cobra.Command, args []string) error {
			dash := cmd.ArgsLenAtDash()
			switch {
			case dash == -1 && len(args) == 1:
				return nil
			case dash == 1:
				return nil
			case dash == -1 && len(args) == 0:
				return fmt.Errorf("requires a node name")
			case dash == -1:
				return fmt.Errorf("extra arguments %v; use -- to separate the remote command", args[1:])
			default:
				return fmt.Errorf("exactly one node name is required before --")
			}
		},
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return cli.CompleteNodeNames(cmd, args, toComplete)
		},
		RunE: runSSH,
	}

	return cmd
}

func runSSH(cmd *cobra.Command, args []string) error {
	nodeName := args[0]

	ctx := context.Background()
	logger := logrus.New()

	clusterName := viper.GetString("cluster.name")
	sshClient := ssh.NewClientForNode(clusterName, nodeName, logger)

	if cmd.ArgsLenAtDash() == 1 {
		command := strings.Join(args[1:], " ")
		exitCode, err := sshClient.ExecStream(ctx, command)
		if err != nil {
			return fmt.Errorf("failed to execute command on %s: %w", nodeName, err)
		}
		if exitCode != 0 {
			return &cli.ExitCodeError{Code: exitCode}
		}
		return nil
	}

	clusterIP := node.CalculateClusterIP(clusterName, nodeName)
	fmt.Printf("Connecting to %s (SSH: %s:%s, cluster: %s) as user core\n",
		nodeName, ssh.DefaultSSHHost, ssh.DefaultSSHPort, clusterIP)

	if err := sshClient.Interactive(ctx); err != nil {
		return fmt.Errorf("failed to connect to %s: %w", nodeName, err)
	}

	return nil
}
