package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/adefemi171/runeward/internal/profile"
	"github.com/spf13/cobra"
)

func newListCmd(configDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List profiles reachable from here",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := *configDir
			if dir == "" {
				dir = os.Getenv("RUNEWARD_CONFIG_DIR")
			}
			names, err := profile.List(profile.Options{ConfigDir: dir})
			if err != nil {
				return err
			}
			if len(names) == 0 {
				fmt.Fprintln(os.Stderr, "no profiles found")
				return nil
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			for _, n := range names {
				p, err := profile.Load(n, profile.Options{ConfigDir: dir})
				if err != nil {
					fmt.Fprintf(tw, "%s\t(error: %v)\n", n, err)
					continue
				}
				egress := "open"
				if p.Network.DenyByDefault() {
					egress = fmt.Sprintf("deny+%d", len(p.Network.Rules))
				}
				fmt.Fprintf(tw, "%s\thost=%s\tegress=%s\n", n, p.Host.Type, egress)
			}
			return tw.Flush()
		},
	}
}
