package controlplane

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/Runewardd/runeward/internal/evidence"
	"github.com/Runewardd/runeward/internal/ledger"
	"github.com/Runewardd/runeward/internal/profile"
)

// Evidence builds the portable, signed evidence document for a Citadel.
func (m *Manager) Evidence(id, version string) (evidence.Document, error) {
	sess, err := m.session(id)
	if err != nil {
		return evidence.Document{}, err
	}
	p := sess.Profile
	var policyView bytes.Buffer
	portableProfile := *p
	portableProfile.Source = filepath.Base(p.Source)
	if err := profile.Print(&policyView, &portableProfile); err != nil {
		return evidence.Document{}, err
	}
	var bundleJSON bytes.Buffer
	if err := m.ExportBundle(&bundleJSON, id); err != nil {
		return evidence.Document{}, err
	}
	var bundle ledger.Bundle
	if err := json.Unmarshal(bundleJSON.Bytes(), &bundle); err != nil {
		return evidence.Document{}, fmt.Errorf("decode Chronicle bundle: %w", err)
	}
	findings := profile.Lint(p)
	portable := make([]evidence.Finding, 0, len(findings))
	for _, finding := range findings {
		portable = append(portable, evidence.Finding{Severity: finding.Severity, Field: finding.Field, Message: finding.Message})
	}
	return evidence.New(version, p.Name, filepath.Base(p.Source), p.Host.Image, policyView.String(), portable, bundle), nil
}
