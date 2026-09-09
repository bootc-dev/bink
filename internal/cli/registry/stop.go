// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"errors"
	"fmt"

	registrypkg "github.com/bootc-dev/bink/internal/registry"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func newStopCmd() *cobra.Command {
	var authOnly bool

	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop and remove the local registries",
		Long:  "Stop both local registry containers and remove the shared data volume. Use --auth to stop only the authenticated registry.",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := registrypkg.NewManager()
			if err != nil {
				return fmt.Errorf("creating registry manager: %w", err)
			}

			authErr := mgr.StopAuthRegistry(cmd.Context())
			if authOnly {
				if authErr != nil {
					return fmt.Errorf("stopping auth registry: %w", authErr)
				}
				logrus.Info("Auth registry stopped and removed")
				return nil
			}

			registryErr := mgr.StopRegistry(cmd.Context())
			if err := errors.Join(authErr, registryErr); err != nil {
				return fmt.Errorf("stopping registries: %w", err)
			}

			logrus.Info("All registries stopped and data removed")
			return nil
		},
	}

	cmd.Flags().BoolVar(&authOnly, "auth", false, "Stop only the authenticated registry")

	return cmd
}
