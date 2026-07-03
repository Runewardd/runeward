package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the runeward version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("runeward", version)
			return nil
		},
	}
}
