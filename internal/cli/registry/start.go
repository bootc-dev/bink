package registry

import (
	"fmt"

	registrypkg "github.com/bootc-dev/bink/internal/registry"
	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the local container registry",
		Long:  "Start the shared local registry container, creating it if it doesn't exist",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := registrypkg.NewManager()
			if err != nil {
				return fmt.Errorf("creating registry manager: %w", err)
			}

			if err := mgr.EnsureRegistry(cmd.Context()); err != nil {
				return fmt.Errorf("starting registry: %w", err)
			}

			return nil
		},
	}

	return cmd
}
