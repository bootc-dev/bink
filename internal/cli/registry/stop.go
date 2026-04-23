package registry

import (
	"fmt"

	registrypkg "github.com/bootc-dev/bink/internal/registry"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func newStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop and remove the local registry",
		Long:  "Stop the shared local registry container and remove its data volume",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := registrypkg.NewManager()
			if err != nil {
				return fmt.Errorf("creating registry manager: %w", err)
			}

			if err := mgr.StopRegistry(cmd.Context()); err != nil {
				return fmt.Errorf("stopping registry: %w", err)
			}

			logrus.Info("Registry stopped and data removed")
			return nil
		},
	}

	return cmd
}
