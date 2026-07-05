package cli

import (
	"fmt"
	"os"

	"github.com/Runewardd/runeward/internal/termrec"
	"github.com/spf13/cobra"
)

func newReplayCmd(configDir *string) *cobra.Command {
	var realtime bool
	var noTiming bool

	cmd := &cobra.Command{
		Use:   "replay <cast-file>",
		Short: "Replay a recorded terminal session (asciinema v2 cast)",
		Long: "Replay a governed terminal session captured as an asciinema v2\n" +
			".cast file. By default timing is honored (idle gaps capped so replay\n" +
			"never stalls); pass --no-timing to dump the output instantly.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			f, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("open cast file: %w", err)
			}
			defer f.Close()

			honorTiming := realtime && !noTiming
			return termrec.Replay(f, cmd.OutOrStdout(), honorTiming)
		},
	}

	cmd.Flags().BoolVar(&realtime, "realtime", true, "honor recorded frame timing")
	cmd.Flags().BoolVar(&noTiming, "no-timing", false, "dump output instantly, ignoring timing")
	return cmd
}
