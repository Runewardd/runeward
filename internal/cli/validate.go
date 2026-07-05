package cli

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/Runewardd/runeward/internal/profile"
	"github.com/spf13/cobra"
)

// newValidateCmd builds the `runeward validate` command: a static linter that
// resolves profiles and reports likely misconfigurations without launching a
// sandbox. With no arguments it validates every resolvable profile; otherwise
// it validates just the named ones. It exits non-zero when any error-severity
// finding is present (and, under --strict, when any warning is present).
func newValidateCmd(configDir *string) *cobra.Command {
	var strict bool
	cmd := &cobra.Command{
		Use:   "validate [profile...]",
		Short: "Statically validate and lint profiles",
		Long: "Resolve profiles and lint them for likely misconfigurations —\n" +
			"missing images, unresolved secret references, dead egress and\n" +
			"policy rules — without launching anything. Exits non-zero on any\n" +
			"error-severity finding; --strict also fails on warnings.",
		Args:         cobra.ArbitraryArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := *configDir
			if dir == "" {
				dir = os.Getenv("RUNEWARD_CONFIG_DIR")
			}

			names := args
			if len(names) == 0 {
				resolved, err := profile.List(profile.Options{ConfigDir: dir})
				if err != nil {
					return err
				}
				names = resolved
			}

			out := cmd.OutOrStdout()
			if len(names) == 0 {
				fmt.Fprintln(out, "no profiles found")
				return nil
			}

			var totalErrors, totalWarnings int
			for _, name := range names {
				findings := validateOne(name, dir)
				for _, f := range findings {
					switch f.Severity {
					case profile.SeverityError:
						totalErrors++
					case profile.SeverityWarn:
						totalWarnings++
					}
				}
				printProfileFindings(out, name, findings)
			}

			if totalErrors > 0 || (strict && totalWarnings > 0) {
				return fmt.Errorf("validation failed: %d error(s), %d warning(s)", totalErrors, totalWarnings)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&strict, "strict", false, "treat warnings as failures (exit non-zero)")
	return cmd
}

// validateOne resolves and lints a single profile, folding a load/parse failure
// into an error-severity finding so it groups with the profile's other output.
func validateOne(name, dir string) []profile.Finding {
	p, err := profile.Load(name, profile.Options{ConfigDir: dir})
	if err != nil {
		return []profile.Finding{{
			Severity: profile.SeverityError,
			Field:    "load",
			Message:  err.Error(),
		}}
	}
	return profile.Lint(p)
}

// printProfileFindings writes one profile's findings to w, grouped under the
// profile name with a severity marker per line. A profile with no findings
// prints an "ok" line.
func printProfileFindings(w io.Writer, name string, findings []profile.Finding) {
	fmt.Fprintf(w, "%s\n", name)
	if len(findings) == 0 {
		fmt.Fprintln(w, "  ok")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, f := range findings {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", severityMarker(f.Severity), f.Field, f.Message)
	}
	tw.Flush()
}

// severityMarker renders a fixed-width label for a finding severity.
func severityMarker(sev string) string {
	switch sev {
	case profile.SeverityError:
		return "[error]"
	case profile.SeverityWarn:
		return "[warn] "
	default:
		return "[" + sev + "]"
	}
}
