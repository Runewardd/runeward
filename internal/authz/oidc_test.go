package authz

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func signedTestJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"alg": "RS256", "kid": kid, "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func TestOIDCVerifyMapsTenantAndScopes(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	verifier := &OIDCVerifier{
		issuer: "https://issuer.example", audience: "runeward", keys: map[string]*rsa.PublicKey{"test": &key.PublicKey},
	}
	token := signedTestJWT(t, key, "test", map[string]any{
		"iss": "https://issuer.example", "sub": "subject-1", "aud": []string{"other", "runeward"},
		"exp": time.Now().Add(time.Minute).Unix(), "preferred_username": "codex-agent",
		"runeward_tenant": "team-alpha", "runeward_profiles": []string{"dev-*"},
		"runeward_approval_profiles": []string{"dev-review"}, "runeward_can_approve": true,
	})
	p, err := verifier.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if p.Name != "codex-agent" || p.TenantID() != "team-alpha" || !p.CanLaunch("dev-one") || !p.CanApproveProfile("dev-review") {
		t.Fatalf("unexpected principal: %#v", p)
	}
}

func TestOIDCVerifyRejectsWrongAudienceAndTampering(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	verifier := &OIDCVerifier{
		issuer: "https://issuer.example", audience: "runeward", keys: map[string]*rsa.PublicKey{"test": &key.PublicKey},
	}
	claims := map[string]any{"iss": "https://issuer.example", "sub": "s", "aud": "wrong", "exp": time.Now().Add(time.Minute).Unix()}
	if _, err := verifier.Verify(signedTestJWT(t, key, "test", claims)); err == nil {
		t.Fatal("expected wrong audience to be rejected")
	}
	claims["aud"] = "runeward"
	token := signedTestJWT(t, key, "test", claims)
	parts := []byte(token)
	parts[len(parts)-1] ^= 1
	if _, err := verifier.Verify(string(parts)); err == nil {
		t.Fatal("expected tampered signature to be rejected")
	}
}

func TestOIDCUnknownKeyRefreshIsRateLimited(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	verifier := &OIDCVerifier{
		issuer: "https://issuer.example", audience: "runeward",
		jwksURL: "not-a-valid-url", keys: map[string]*rsa.PublicKey{}, lastRefresh: time.Now(),
	}
	token := signedTestJWT(t, key, "attacker-controlled-kid", map[string]any{
		"iss": "https://issuer.example", "sub": "s", "aud": "runeward", "exp": time.Now().Add(time.Minute).Unix(),
	})
	if _, err := verifier.Verify(token); err == nil || err.Error() != "OIDC signing key not found" {
		t.Fatalf("unexpected cache-miss result: %v", err)
	}
}

func TestOIDCRejectsOversizedToken(t *testing.T) {
	verifier := &OIDCVerifier{}
	if _, err := verifier.Verify(string(make([]byte, 32*1024+1))); err == nil {
		t.Fatal("expected oversized token to be rejected")
	}
}

func TestRequireSecureOIDCEndpoint(t *testing.T) {
	for _, endpoint := range []string{"https://issuer.example", "http://127.0.0.1:8080", "http://localhost:8080"} {
		if err := requireSecureOIDCEndpoint(endpoint); err != nil {
			t.Fatalf("%s: %v", endpoint, err)
		}
	}
	if err := requireSecureOIDCEndpoint("http://issuer.example"); err == nil {
		t.Fatal("expected non-loopback HTTP issuer to be rejected")
	}
}
