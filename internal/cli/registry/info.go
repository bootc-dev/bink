package registry

import (
	"fmt"

	registrypkg "github.com/bootc-dev/bink/internal/registry"
	"github.com/containers/podman/v5/libpod/define"
	"github.com/spf13/cobra"
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

			fmt.Printf("Registry:  %s\n", status)
			fmt.Printf("IP:        %s\n", info.IP)
			fmt.Printf("Host port: %d\n", info.HostPort)
			fmt.Printf("Push:      podman push --tls-verify=false %s/<image>:<tag>\n", info.PushURL)
			fmt.Printf("Pull:      %s/<image>:<tag>\n", info.PullURL)

			return nil
		},
	}

	return cmd
}
