// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package node

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newJoinCmd() *cobra.Command {
	var controlPlane string

	cmd := &cobra.Command{
		Use:   "join <name>",
		Short: "Join a worker node to the cluster",
		Long:  "Create a new worker node and join it to the cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeName := args[0]
			_ = nodeName
			_ = controlPlane
			return fmt.Errorf("not implemented yet")
		},
	}

	cmd.Flags().StringVar(&controlPlane, "control-plane", "node1", "Control plane node to join to")

	return cmd
}
