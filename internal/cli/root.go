// Package cli implements the runeward command-line surface.
package cli

import (
	"github.com/spf13/cobra"
)

// version is overridden at build time via -ldflags.
var version = "dev"

// reserved lists the real subcommands. Any first argument that is not reserved
// is treated as a profile name to enter (so `runeward work` works alongside
// `runeward serve`).
var reserved = map[string]bool{
	"serve":      true,
	"mcp":        true,
	"list":       true,
	"print":      true,
	"version":    true,
	"enter":      true,
	"export":     true,
	"audit":      true,
	"bundle":     true,
	"controller": true,
	"webhook":    true,
	"up":         true,
	"help":       true,
	"completion": true,
	"-h":         true,
	"--help":     true,
}

// Execute parses args and runs the CLI. Unknown leading tokens are rewritten to
// the `enter` subcommand so a bare profile name is a valid invocation.
func Execute(args []string) error {
	root := newRootCmd()
	root.SetArgs(rewriteForEnter(args))
	return root.Execute()
}

// rewriteForEnter injects "enter" before the first non-flag token when that
// token is not a reserved subcommand.
func rewriteForEnter(args []string) []string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if len(a) > 0 && a[0] == '-' {
			// Global flags that take a value would need to skip the value; the
			// only such flag today is --config-dir, handled generically below.
			if a == "--config-dir" || a == "-c" {
				i++ // skip the value
			}
			continue
		}
		if reserved[a] {
			return args
		}
		// Insert "enter" at position i.
		out := make([]string, 0, len(args)+1)
		out = append(out, args[:i]...)
		out = append(out, "enter")
		out = append(out, args[i:]...)
		return out
	}
	return args
}

func newRootCmd() *cobra.Command {
	var configDir string

	root := &cobra.Command{
		Use:           "runeward",
		Short:         "Governed execution cells for AI agents",
		Long:          "runeward resolves declarative TOML profiles and provisions isolated,\ngoverned sandboxes (Docker or Kubernetes) for AI agents.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	root.PersistentFlags().StringVarP(&configDir, "config-dir", "c", "",
		"pin profile resolution to a single directory (or $RUNEWARD_CONFIG_DIR)")

	root.AddCommand(
		newEnterCmd(&configDir),
		newExportCmd(),
		newPrintCmd(&configDir),
		newListCmd(&configDir),
		newServeCmd(&configDir),
		newMCPCmd(&configDir),
		newControllerCmd(&configDir),
		newWebhookCmd(&configDir),
		newUpCmd(),
		newAuditCmd(),
		newBundleCmd(),
		newVersionCmd(),
	)
	return root
}
