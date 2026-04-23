package registry

import (
	"github.com/spf13/cobra"
)

func NewRegistryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Manage the local container registry",
		Long:  "Start, stop, and inspect the shared local container registry",
	}

	cmd.AddCommand(newStartCmd())
	cmd.AddCommand(newStopCmd())
	cmd.AddCommand(newInfoCmd())

	return cmd
}
