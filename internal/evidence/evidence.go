// Package evidence defines the portable, independently verifiable artifact
// produced by runeward after an agent run.
package evidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/Runewardd/runeward/internal/ledger"
)

const Format = "runeward-evidence/v1"

// Finding is a portable policy-lint finding captured at export time.
type Finding struct {
	Severity string `json:"severity"`
	Field    string `json:"field"`
	Message  string `json:"message"`
}

// PolicySnapshot records the resolved, secret-redacted policy that governed a
// run. SHA256 makes accidental or malicious edits independently detectable.
type PolicySnapshot struct {
	Name     string    `json:"name"`
	Source   string    `json:"source,omitempty"`
	Image    string    `json:"image,omitempty"`
	Resolved string    `json:"resolved"`
	SHA256   string    `json:"sha256"`
	Findings []Finding `json:"findings,omitempty"`
}

// Document combines policy context with the signed Chronicle audit bundle.
// The embedded ledger public key verifies event integrity; organizations can
// pin that key out-of-band when they need identity assurance as well.
type Document struct {
	Format          string         `json:"format"`
	GeneratedAt     time.Time      `json:"generated_at"`
	RunewardVersion string         `json:"runeward_version"`
	Policy          PolicySnapshot `json:"policy"`
	Chronicle       ledger.Bundle  `json:"chronicle"`
}

// New creates an evidence document and hashes the resolved policy snapshot.
func New(version, name, source, image, resolved string, findings []Finding, bundle ledger.Bundle) Document {
	sum := sha256.Sum256([]byte(resolved))
	return Document{
		Format:          Format,
		GeneratedAt:     time.Now().UTC(),
		RunewardVersion: version,
		Policy: PolicySnapshot{
			Name: name, Source: source, Image: image, Resolved: resolved,
			SHA256: hex.EncodeToString(sum[:]), Findings: findings,
		},
		Chronicle: bundle,
	}
}

// Write serializes a document as stable, human-inspectable JSON.
func Write(w io.Writer, doc Document) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("evidence: encode: %w", err)
	}
	return nil
}

// Read decodes an evidence document.
func Read(r io.Reader) (Document, error) {
	var doc Document
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return Document{}, fmt.Errorf("evidence: decode: %w", err)
	}
	return doc, nil
}

// Verify checks the document format, policy digest, event hash chain, and every
// event signature. It returns the number of verified events.
func Verify(doc Document) (int, error) {
	if doc.Format != Format {
		return 0, fmt.Errorf("evidence: unsupported format %q", doc.Format)
	}
	sum := sha256.Sum256([]byte(doc.Policy.Resolved))
	if got := hex.EncodeToString(sum[:]); got != doc.Policy.SHA256 {
		return 0, fmt.Errorf("evidence: policy snapshot hash mismatch")
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(doc.Chronicle); err != nil {
		return 0, fmt.Errorf("evidence: encode Chronicle: %w", err)
	}
	return ledger.VerifyBundle(&buf)
}
