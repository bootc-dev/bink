// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package registry

import (
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

			if err := mgr.StopAuthRegistry(cmd.Context()); err != nil {
				if authOnly {
					return fmt.Errorf("stopping auth registry: %w", err)
				}
				logrus.Warnf("Failed to stop auth registry: %v", err)
			}

			if authOnly {
				logrus.Info("Auth registry stopped and removed")
				return nil
			}

			if err := mgr.StopRegistry(cmd.Context()); err != nil {
				return fmt.Errorf("stopping registry: %w", err)
			}

			logrus.Info("All registries stopped and data removed")
			return nil
		},
	}

	cmd.Flags().BoolVar(&authOnly, "auth", false, "Stop only the authenticated registry")

	return cmd
}
