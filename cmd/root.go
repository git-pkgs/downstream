package cmd

import (
	"github.com/spf13/cobra"
)

var version = "dev"

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "downstream",
		Short:         "Test a library against its dependents",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	addTestCmd(root)
	addListCmd(root)
	addDiscoverCmd(root)
	addRunCmd(root)

	return root
}
