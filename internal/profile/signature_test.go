package profile

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func mustKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

func TestSignVerifyRoundTrip(t *testing.T) {
	pub, priv := mustKey(t)
	content := []byte("name = \"demo\"\n[network]\nallow = [\"example.com\"]\n")

	sig := Sign(content, priv)
	if err := Verify(content, sig, pub); err != nil {
		t.Fatalf("Verify on valid signature: %v", err)
	}
}

func TestVerifyTamperedContentFails(t *testing.T) {
	pub, priv := mustKey(t)
	content := []byte("name = \"demo\"\n")

	sig := Sign(content, priv)
	tampered := append([]byte{}, content...)
	tampered[0] ^= 0xff
	if err := Verify(tampered, sig, pub); err == nil {
		t.Fatal("Verify accepted tampered content")
	}
}

func TestVerifyWrongKeyFails(t *testing.T) {
	_, priv := mustKey(t)
	otherPub, _ := mustKey(t)
	content := []byte("name = \"demo\"\n")

	sig := Sign(content, priv)
	if err := Verify(content, sig, otherPub); err == nil {
		t.Fatal("Verify accepted signature under the wrong key")
	}
}

func TestSignatureEnvelopeRoundTrip(t *testing.T) {
	pub, priv := mustKey(t)
	content := []byte("name = \"demo\"\nreadonly = true\n")

	env := NewSignature(content, priv)
	if env.Format != SignatureFormat {
		t.Fatalf("format = %q, want %q", env.Format, SignatureFormat)
	}

	raw, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed, err := ParseSignature(raw)
	if err != nil {
		t.Fatalf("ParseSignature: %v", err)
	}

	keyID, err := parsed.Verify(content, pub)
	if err != nil {
		t.Fatalf("envelope Verify: %v", err)
	}
	if keyID != env.KeyID {
		t.Fatalf("verified key id = %q, want %q", keyID, env.KeyID)
	}
}

func TestSignatureEnvelopeTamperedFails(t *testing.T) {
	pub, priv := mustKey(t)
	content := []byte("name = \"demo\"\n")

	env := NewSignature(content, priv)
	if _, err := env.Verify([]byte("name = \"evil\"\n"), pub); err == nil {
		t.Fatal("envelope Verify accepted tampered content")
	}
}

func TestSignatureEnvelopeWrongKeyFails(t *testing.T) {
	_, priv := mustKey(t)
	otherPub, _ := mustKey(t)
	content := []byte("name = \"demo\"\n")

	env := NewSignature(content, priv)
	if _, err := env.Verify(content, otherPub); err == nil {
		t.Fatal("envelope Verify accepted the wrong verify key")
	}
}

func TestParseSignatureRejectsEmptySig(t *testing.T) {
	if _, err := ParseSignature([]byte(`{"format":"runeward.profile.sig.v1","key_id":"abcd"}`)); err == nil {
		t.Fatal("ParseSignature accepted an envelope with no sig")
	}
}
