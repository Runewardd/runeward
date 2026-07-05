package profile

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/Runewardd/runeward/internal/policybundle"
)

// SignatureFormat is the schema tag written into detached signature files so a
// reader can tell a runeward profile signature apart from other JSON.
const SignatureFormat = "runeward.profile.sig.v1"

// Signature is the JSON envelope written to a detached ".sig" file next to a
// signed profile. It carries the base64 ed25519 signature over the exact
// profile file bytes on disk plus the signer's key id, so verification can
// report which key was used and reject an obviously mismatched key early.
//
// The signed message is the raw profile bytes verbatim: signing the exact
// on-disk bytes keeps the scheme canonical (no re-serialization) and mirrors
// how policybundle covers the raw policy layer.
type Signature struct {
	// Format is the envelope schema tag; see SignatureFormat.
	Format string `json:"format"`
	// KeyID is the short fingerprint of the signing public key
	// (policybundle.KeyID).
	KeyID string `json:"key_id"`
	// Sig is the base64 (std) ed25519 signature over the profile bytes.
	Sig string `json:"sig"`
}

// Sign returns the raw ed25519 signature over the exact profile bytes.
func Sign(content []byte, priv ed25519.PrivateKey) []byte {
	return ed25519.Sign(priv, content)
}

// Verify returns nil when sig is a valid ed25519 signature over content for
// pub, and an error otherwise.
func Verify(content, sig []byte, pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("profile: public key wrong size %d (want %d)", len(pub), ed25519.PublicKeySize)
	}
	if !ed25519.Verify(pub, content, sig) {
		return fmt.Errorf("profile: signature does not verify against the supplied key")
	}
	return nil
}

// NewSignature signs content and wraps the result in a detached [Signature]
// envelope tagged with the signer's key id.
func NewSignature(content []byte, priv ed25519.PrivateKey) Signature {
	pub := priv.Public().(ed25519.PublicKey)
	return Signature{
		Format: SignatureFormat,
		KeyID:  policybundle.KeyID(pub),
		Sig:    base64.StdEncoding.EncodeToString(Sign(content, priv)),
	}
}

// Marshal encodes the signature envelope as indented JSON (with a trailing
// newline) suitable for writing to a detached ".sig" file.
func (s Signature) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("profile: marshal signature: %w", err)
	}
	return append(b, '\n'), nil
}

// ParseSignature decodes a detached signature file produced by
// [Signature.Marshal].
func ParseSignature(b []byte) (Signature, error) {
	var s Signature
	if err := json.Unmarshal(b, &s); err != nil {
		return Signature{}, fmt.Errorf("profile: decode signature file: %w", err)
	}
	if s.Sig == "" {
		return Signature{}, fmt.Errorf("profile: signature file missing sig field")
	}
	return s, nil
}

// Verify checks the detached envelope against content and pub. It fails closed
// when the envelope key id does not match pub, then verifies the signature,
// returning the verified key id on success.
func (s Signature) Verify(content []byte, pub ed25519.PublicKey) (string, error) {
	sig, err := base64.StdEncoding.DecodeString(s.Sig)
	if err != nil {
		return "", fmt.Errorf("profile: decode signature: %w", err)
	}
	wantID := policybundle.KeyID(pub)
	if s.KeyID != "" && s.KeyID != wantID {
		return "", fmt.Errorf("profile: signature key id %q does not match verify key %q", s.KeyID, wantID)
	}
	if err := Verify(content, sig, pub); err != nil {
		return "", err
	}
	return wantID, nil
}
