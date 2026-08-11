// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"fmt"

	registrypkg "github.com/bootc-dev/bink/internal/registry"
	"github.com/spf13/cobra"
	"go.podman.io/podman/v6/libpod/define"
)

func newInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Show registry status and connection details",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := registrypkg.NewManager()
			if err != nil {
				return fmt.Errorf("creating registry manager: %w", err)
			}

			info, err := mgr.RegistryInfo(cmd.Context())
			if err != nil {
				return fmt.Errorf("getting registry info: %w", err)
			}

			status := "stopped"
			if info.Running {
				status = define.ContainerStateRunning.String()
			}

			fmt.Printf("Registry (unauthenticated): %s\n", status)
			fmt.Printf("  IP:        %s\n", info.IP)
			fmt.Printf("  Host port: %d\n", info.HostPort)
			fmt.Printf("  Push:      podman push --tls-verify=false %s/<image>:<tag>\n", info.PushURL)
			fmt.Printf("  Pull:      %s/<image>:<tag>\n", info.PullURL)
			fmt.Println()

			authInfo, err := mgr.AuthRegistryInfo(cmd.Context())
			if err != nil {
				return fmt.Errorf("getting auth registry info: %w", err)
			}

			authStatus := "stopped"
			if authInfo.Running {
				authStatus = define.ContainerStateRunning.String()
			}

			fmt.Printf("Registry (authenticated):   %s\n", authStatus)
			fmt.Printf("  IP:          %s\n", authInfo.IP)
			fmt.Printf("  Host port:   %d\n", authInfo.HostPort)
			fmt.Printf("  Pull:        %s/<image>:<tag>\n", authInfo.PullURL)
			fmt.Printf("  Credentials: %s / %s\n", authInfo.Username, authInfo.Password)

			return nil
		},
	}

	return cmd
}
