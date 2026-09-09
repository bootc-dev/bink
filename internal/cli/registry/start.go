// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"fmt"

	registrypkg "github.com/bootc-dev/bink/internal/registry"
	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	var authOnly bool
	var registryUser string
	var registryPassword string

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the local container registry",
		Long:  "Start the shared local registry containers, creating them if they don't exist",
		RunE: func(cmd *cobra.Command, args []string) error {
			authRequested, err := registrypkg.AuthRegistryRequested(registryUser, registryPassword)
			if err != nil {
				return fmt.Errorf("invalid auth registry credentials: %w", err)
			}
			if authOnly && !authRequested {
				return fmt.Errorf("invalid auth registry credentials: registry username and password are required with --auth")
			}

			mgr, err := registrypkg.NewManager()
			if err != nil {
				return fmt.Errorf("creating registry manager: %w", err)
			}

			if !authOnly {
				if err := mgr.EnsureRegistry(cmd.Context()); err != nil {
					return fmt.Errorf("starting registry: %w", err)
				}
			}

			if authRequested {
				if err := mgr.EnsureAuthRegistry(cmd.Context(), registryUser, registryPassword); err != nil {
					return fmt.Errorf("starting auth registry: %w", err)
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&authOnly, "auth", false, "Start only the authenticated registry")
	cmd.Flags().StringVar(&registryUser, "registry-user", "", "Username for the authenticated registry")
	cmd.Flags().StringVar(&registryPassword, "registry-password", "", "Password for the authenticated registry")

	return cmd
}
