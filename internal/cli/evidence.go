package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Runewardd/runeward/internal/controlplane"
	"github.com/Runewardd/runeward/internal/evidence"
	"github.com/Runewardd/runeward/internal/ledger"
	"github.com/Runewardd/runeward/internal/profile"
	"github.com/spf13/cobra"
)

func newEvidenceCmd(configDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "evidence",
		Short: "Export or verify a portable policy and signed-audit package",
	}
	cmd.AddCommand(newEvidenceExportCmd(configDir), newEvidenceVerifyCmd())
	return cmd
}

func newEvidenceExportCmd(configDir *string) *cobra.Command {
	var sessionID, output string
	var force bool
	cmd := &cobra.Command{
		Use:   "export <charter>",
		Short: "Package the resolved policy and signed audit trail as JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProfile(args[0], *configDir)
			if err != nil {
				return err
			}
			mgr, err := controlplane.New(resolveConfigDir(*configDir))
			if err != nil {
				return fmt.Errorf("open local audit state: %w (stop a running server first, or download evidence from its dashboard)", err)
			}
			defer mgr.Close()
			doc, err := buildEvidenceDocument(mgr, p, sessionID)
			if err != nil {
				return err
			}
			w, closeFn, err := evidenceOutput(cmd, output, force)
			if err != nil {
				return err
			}
			if closeFn != nil {
				defer closeFn()
			}
			if err := evidence.Write(w, doc); err != nil {
				return err
			}
			if output != "" && output != "-" {
				fmt.Fprintf(cmd.ErrOrStderr(), "evidence package: %s\n", output)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "include only one sandbox/session (default: all events)")
	cmd.Flags().StringVarP(&output, "output", "o", "-", "write to a file, or - for stdout")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing output file")
	return cmd
}

func buildEvidenceDocument(mgr *controlplane.Manager, p *profile.Profile, sessionID string) (evidence.Document, error) {
	var policyView bytes.Buffer
	portableProfile := *p
	portableProfile.Source = filepath.Base(p.Source)
	if err := profile.Print(&policyView, &portableProfile); err != nil {
		return evidence.Document{}, fmt.Errorf("render policy for evidence: %w", err)
	}
	var bundleJSON bytes.Buffer
	if err := mgr.ExportBundle(&bundleJSON, sessionID); err != nil {
		return evidence.Document{}, err
	}
	var bundle ledger.Bundle
	if err := json.Unmarshal(bundleJSON.Bytes(), &bundle); err != nil {
		return evidence.Document{}, fmt.Errorf("decode signed audit bundle: %w", err)
	}
	findings := profile.Lint(p)
	portable := make([]evidence.Finding, 0, len(findings))
	for _, finding := range findings {
		portable = append(portable, evidence.Finding{
			Severity: finding.Severity, Field: finding.Field, Message: finding.Message,
		})
	}
	return evidence.New(version, p.Name, filepath.Base(p.Source), p.Host.Image, policyView.String(), portable, bundle), nil
}

func evidenceOutput(cmd *cobra.Command, output string, force bool) (io.Writer, func() error, error) {
	if output == "" || output == "-" {
		return cmd.OutOrStdout(), nil, nil
	}
	flags := os.O_CREATE | os.O_WRONLY
	if force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	// #nosec G304 -- output is an explicit destination selected by the local
	// CLI operator; O_EXCL prevents accidental replacement unless --force is set.
	f, err := os.OpenFile(output, flags, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, nil, fmt.Errorf("output %q already exists; pass --force to replace it", output)
		}
		return nil, nil, err
	}
	return f, f.Close, nil
}

func newEvidenceVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify <evidence.json>",
		Short: "Verify the policy digest, hash chain, and every event signature",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			doc, err := evidence.Read(f)
			if err != nil {
				return err
			}
			count, err := evidence.Verify(doc)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "verified: policy digest and %d signed audit event(s); key %s\n", count, strings.TrimSpace(doc.Chronicle.KeyID))
			return nil
		},
	}
}
