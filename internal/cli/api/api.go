// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"github.com/spf13/cobra"
)

func NewAPICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Manage Kubernetes API server access",
		Long:  "Expose and manage access to the Kubernetes API server",
	}

	cmd.AddCommand(newExposeCmd())

	return cmd
}
