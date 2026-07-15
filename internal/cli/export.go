package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Runewardd/runeward/internal/backend"
	"github.com/spf13/cobra"
)

func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export <citadel-id> <dest-dir>",
		Short: "Copy a Citadel's workspace back out to a host directory",
		Long: "Export copies the /workspace of a running Citadel (sandbox; Docker or Kubernetes)\n" +
			"into a local directory. The Citadel is only read; the destination receives\n" +
			"a point-in-time copy of the agent's results, so later host edits never flow\n" +
			"back into the Citadel. This is the inverse of a Charter's host.copy_from.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			dest, err := filepath.Abs(expandHome(args[1]))
			if err != nil {
				return err
			}
			if err := backend.Export(cmd.Context(), id, dest); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "runeward: exported citadel %s workspace -> %s\n", id, dest)
			return nil
		},
	}
	return cmd
}
