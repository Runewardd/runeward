package cli

import (
	"github.com/Runewardd/runeward/internal/profile"
	"github.com/spf13/cobra"
)

func newPrintCmd(configDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "print <charter>",
		Short: "Render a policy file's resolved controls (redacted)",
		Long:  "Resolve a policy file (Charter) and print its controls and projected env with secrets\nredacted, so you can inspect the sandbox before starting it.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProfile(args[0], *configDir)
			if err != nil {
				return err
			}
			return profile.Print(cmd.OutOrStdout(), p)
		},
	}
}
