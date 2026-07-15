package cli

import (
	"os"

	"github.com/Runewardd/runeward/internal/profile"
	"github.com/spf13/cobra"
)

func newPrintCmd(configDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "print <charter>",
		Short: "Render a Charter's resolved policy (redacted)",
		Long:  "Resolve a Charter and print its policy and projected env with secrets\nredacted, so you can read the box before stepping into it.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProfile(args[0], *configDir)
			if err != nil {
				return err
			}
			profile.Print(os.Stdout, p)
			return nil
		},
	}
}
