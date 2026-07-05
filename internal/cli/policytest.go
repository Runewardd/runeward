package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/Runewardd/runeward/internal/policy"
	"github.com/Runewardd/runeward/internal/policytemplates"
	"github.com/Runewardd/runeward/internal/profile"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

// newPolicyCmd is the "policy" command group. It hosts "test" (offline policy
// simulation) and "scaffold" (ready-made policy templates); it is a parent so
// future policy tooling (lint, explain, …) can hang off the same namespace.
func newPolicyCmd(configDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "Author, inspect, and test authority policies",
	}
	cmd.AddCommand(newPolicyTestCmd(configDir))
	cmd.AddCommand(newPolicyScaffoldCmd())
	return cmd
}

// newPolicyScaffoldCmd prints ready-made policy snippets for common controls so
// operators don't hand-write policy from scratch. With no template name it
// lists the available templates; with a name it prints the TOML to stdout so it
// can be redirected into a profile or appended with `>>`.
func newPolicyScaffoldCmd() *cobra.Command {
	var list bool
	cmd := &cobra.Command{
		Use:   "scaffold [template]",
		Short: "Print a ready-made policy template for a common control",
		Long: "Print a ready-made policy snippet for a common control (deny prod\n" +
			"mutations, gate package installs, confine egress, …). Run without a\n" +
			"template name (or with --list) to see what's available, then paste the\n" +
			"output into a profile:\n\n" +
			"  runeward policy scaffold package-approval >> myprofile.toml\n",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			if list || len(args) == 0 {
				fmt.Fprintln(w, "Available policy templates:")
				for _, t := range policytemplates.All() {
					fmt.Fprintf(w, "  %-24s %s\n", t.Name, t.Title)
				}
				fmt.Fprintln(w, "\nPrint one with: runeward policy scaffold <name>")
				return nil
			}
			body, err := policytemplates.Render(args[0])
			if err != nil {
				return fmt.Errorf("%w (see: runeward policy scaffold --list)", err)
			}
			fmt.Fprintln(w, body)
			return nil
		},
	}
	cmd.Flags().BoolVar(&list, "list", false, "list available templates")
	return cmd
}

// policyCase is a single sample action asserted against a profile's policy.
// It maps to a [[case]] table in the --cases TOML file.
type policyCase struct {
	// Name is an optional label used in the PASS/FAIL output.
	Name string `toml:"name"`
	// Tool is the action surface (e.g. "shell", "file.read", "net").
	Tool string `toml:"tool"`
	// Action is the primary argument (command line, path, or hostname). It is
	// spelled "action" in the cases file; "arg" is accepted as an alias.
	Action string `toml:"action"`
	Arg    string `toml:"arg"`
	// Args is the optional raw argv vector for argv-aware rules.
	Args []string `toml:"args"`
	// Expect is the awaited verdict: allow | deny | require-approval.
	Expect string `toml:"expect"`
}

// casesFile is the top-level shape of the --cases TOML document.
type casesFile struct {
	Cases []policyCase `toml:"case"`
}

func newPolicyTestCmd(configDir *string) *cobra.Command {
	var (
		casesPath string
		inline    []string
	)
	cmd := &cobra.Command{
		Use:   "test <profile> --cases <file>",
		Short: "Simulate a profile's policy against a table of sample actions",
		Long: "Load a profile's policy and evaluate it offline against a table of\n" +
			"sample actions, asserting the verdict of each. Cases come from a TOML\n" +
			"file (--cases) and/or inline --case flags. Exits non-zero if any case\n" +
			"fails, so policy rules can be unit-tested in CI.\n\n" +
			"Cases file schema:\n\n" +
			"  [[case]]\n" +
			"  name   = \"block rm -rf\"   # optional label\n" +
			"  tool   = \"shell\"\n" +
			"  action = \"rm -rf /\"\n" +
			"  args   = [\"rm\", \"-rf\", \"/\"]  # optional argv vector\n" +
			"  expect = \"deny\"            # allow | deny | require-approval\n",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProfile(args[0], *configDir)
			if err != nil {
				return err
			}

			engine, err := policyEngineForProfile(p)
			if err != nil {
				return err
			}

			cases, err := collectPolicyCases(casesPath, inline)
			if err != nil {
				return err
			}
			if len(cases) == 0 {
				return fmt.Errorf("no cases provided; pass --cases <file> and/or --case tool=...,action=...,expect=...")
			}

			return runPolicyCases(cmd, p.Name, engine, cases)
		},
	}
	cmd.Flags().StringVar(&casesPath, "cases", "", "path to a TOML file of [[case]] assertions")
	cmd.Flags().StringArrayVar(&inline, "case", nil, "inline case as key=value pairs, e.g. tool=shell,action=rm -rf /,expect=deny (repeatable)")
	return cmd
}

// policyEngineForProfile builds the policy engine for a profile using only the
// exported policy constructors. It mirrors controlplane's newEngine, minus the
// bundle path: a signed OCI bundle is remote, so it cannot be simulated offline.
func policyEngineForProfile(p *profile.Profile) (policy.Evaluator, error) {
	switch {
	case p.UsesPolicyBundle():
		return nil, fmt.Errorf("profile %q sources its policy from an OCI policy bundle (%s); bundle-backed policies cannot be simulated offline", p.Name, p.PolicyBundle.Ref)
	case p.UsesRego():
		module := p.Rego.Module
		if module == "" && p.Rego.File != "" {
			b, err := os.ReadFile(expandHome(p.Rego.File))
			if err != nil {
				return nil, fmt.Errorf("read rego policy %q: %w", p.Rego.File, err)
			}
			module = string(b)
		}
		return policy.NewRego(module, p.Rego.Query, profile.VerdictAllow)
	case p.UsesCEL():
		return policy.NewCEL(p.CEL, profile.VerdictAllow)
	default:
		return policy.New(p.Policy, profile.VerdictAllow), nil
	}
}

// collectPolicyCases loads cases from the optional TOML file and appends any
// inline --case flags, preserving file-then-inline order.
func collectPolicyCases(casesPath string, inline []string) ([]policyCase, error) {
	var cases []policyCase
	if casesPath != "" {
		b, err := os.ReadFile(expandHome(casesPath))
		if err != nil {
			return nil, fmt.Errorf("read cases file: %w", err)
		}
		var doc casesFile
		if err := toml.Unmarshal(b, &doc); err != nil {
			return nil, fmt.Errorf("parse cases file %q: %w", casesPath, err)
		}
		cases = append(cases, doc.Cases...)
	}
	for _, spec := range inline {
		c, err := parseInlineCase(spec)
		if err != nil {
			return nil, err
		}
		cases = append(cases, c)
	}
	return cases, nil
}

// parseInlineCase parses a "key=value,key=value" --case flag. The value of the
// last recognized key wins on repeats; commas always separate pairs, so a value
// containing a comma should be supplied via --cases instead.
func parseInlineCase(spec string) (policyCase, error) {
	var c policyCase
	for _, pair := range strings.Split(spec, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return policyCase{}, fmt.Errorf("invalid --case %q: expected key=value pairs", spec)
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch k {
		case "name":
			c.Name = v
		case "tool":
			c.Tool = v
		case "action", "arg":
			c.Action = v
		case "expect":
			c.Expect = v
		default:
			return policyCase{}, fmt.Errorf("invalid --case %q: unknown key %q", spec, k)
		}
	}
	return c, nil
}

// runPolicyCases evaluates every case, prints a PASS/FAIL line for each and a
// summary, and returns a non-zero error when any case fails.
func runPolicyCases(cmd *cobra.Command, profileName string, engine policy.Evaluator, cases []policyCase) error {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "policy test: profile %q, %d case(s)\n", profileName, len(cases))

	failed := 0
	for i, c := range cases {
		want, err := normalizeVerdict(c.Expect)
		if err != nil {
			return fmt.Errorf("case %s: %w", caseLabel(c, i), err)
		}
		arg := c.Action
		if arg == "" {
			arg = c.Arg
		}
		got := engine.Evaluate(policy.Action{Tool: c.Tool, Arg: arg, Args: c.Args}).Verdict

		status := "PASS"
		if got != want {
			status = "FAIL"
			failed++
		}
		fmt.Fprintf(w, "  %s  %s\ttool=%s action=%q expect=%s got=%s\n",
			status, caseLabel(c, i), c.Tool, arg, want, got)
	}

	fmt.Fprintf(w, "%d passed, %d failed\n", len(cases)-failed, failed)
	if failed > 0 {
		return fmt.Errorf("%d of %d policy case(s) failed", failed, len(cases))
	}
	return nil
}

// caseLabel returns a stable identifier for a case in output: its name if set,
// otherwise a 1-based index.
func caseLabel(c policyCase, i int) string {
	if strings.TrimSpace(c.Name) != "" {
		return c.Name
	}
	return fmt.Sprintf("#%d", i+1)
}

// normalizeVerdict maps user-facing verdict spellings to the canonical
// profile.Verdict values, tolerating a few common aliases.
func normalizeVerdict(s string) (profile.Verdict, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "allow":
		return profile.VerdictAllow, nil
	case "deny":
		return profile.VerdictDeny, nil
	case "require-approval", "require_approval", "approval", "approve":
		return profile.VerdictRequireApprove, nil
	case "":
		return "", fmt.Errorf("missing expect verdict (allow|deny|require-approval)")
	default:
		return "", fmt.Errorf("unknown expect verdict %q (allow|deny|require-approval)", s)
	}
}
