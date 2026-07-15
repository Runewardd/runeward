package cli

import (
	"fmt"
	"os"

	"github.com/Runewardd/runeward/internal/profile"
	"github.com/spf13/cobra"
)

// newProfileCmd provides detached ed25519 signing/verification for Charter
// (profile) TOML files (provenance). It reuses the keypairs produced by
// `runeward archive keygen` and the shared loadPrivateKey/loadPublicKey helpers.
func newProfileCmd(configDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "charter",
		Short: "Sign and verify Charter (profile) TOML files (provenance)",
	}
	cmd.AddCommand(newProfileSignCmd(), newProfileVerifyCmd())
	return cmd
}

func newProfileSignCmd() *cobra.Command {
	var (
		keyFile string
		out     string
	)
	cmd := &cobra.Command{
		Use:   "sign <file>",
		Short: "Sign a Charter TOML file, writing a detached signature",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if keyFile == "" {
				return fmt.Errorf("--key is required")
			}
			content, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("read charter: %w", err)
			}
			priv, err := loadPrivateKey(keyFile)
			if err != nil {
				return err
			}
			sig := profile.NewSignature(content, priv)
			sigBytes, err := sig.Marshal()
			if err != nil {
				return err
			}
			if out == "-" {
				_, err := cmd.OutOrStdout().Write(sigBytes)
				return err
			}
			sigPath := out
			if sigPath == "" {
				sigPath = args[0] + ".sig"
			}
			if err := os.WriteFile(sigPath, sigBytes, 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "signed %s\nkey-id:    %s\nsignature: %s\n", args[0], sig.KeyID, sigPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&keyFile, "key", "", "path to a base64 ed25519 private key file (from `archive keygen`)")
	cmd.Flags().StringVar(&out, "out", "", "write the detached signature here (default <file>.sig; use - for stdout)")
	return cmd
}

func newProfileVerifyCmd() *cobra.Command {
	var (
		verifyKey string
		sigFile   string
	)
	cmd := &cobra.Command{
		Use:   "verify <file>",
		Short: "Verify a Charter TOML file against a detached signature",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if verifyKey == "" {
				return fmt.Errorf("--verify-key is required")
			}
			content, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("read charter: %w", err)
			}
			sigPath := sigFile
			if sigPath == "" {
				sigPath = args[0] + ".sig"
			}
			sigBytes, err := os.ReadFile(sigPath)
			if err != nil {
				return fmt.Errorf("read signature: %w", err)
			}
			sig, err := profile.ParseSignature(sigBytes)
			if err != nil {
				return err
			}
			pub, err := loadPublicKey(verifyKey)
			if err != nil {
				return err
			}
			keyID, err := sig.Verify(content, pub)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "verified: %s\n", keyID)
			return nil
		},
	}
	cmd.Flags().StringVar(&verifyKey, "verify-key", "", "base64 ed25519 public key (or a path to a file containing one)")
	cmd.Flags().StringVar(&sigFile, "sig", "", "path to the detached signature file (default <file>.sig)")
	return cmd
}
