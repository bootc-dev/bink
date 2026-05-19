package node

import (
	"github.com/spf13/cobra"
)

func NewNodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Manage cluster nodes",
		Long:  "Manage cluster nodes including adding workers and SSH access",
	}

	cmd.AddCommand(newAddCmd())
	cmd.AddCommand(newJoinCmd())
	cmd.AddCommand(newRemoveCmd())
	cmd.AddCommand(newSSHCmd())
	cmd.AddCommand(newListCmd())

	return cmd
}
