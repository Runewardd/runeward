package authz

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	EnvOIDCIssuer   = "RUNEWARD_OIDC_ISSUER"
	EnvOIDCAudience = "RUNEWARD_OIDC_AUDIENCE"
	EnvOIDCJWKSURL  = "RUNEWARD_OIDC_JWKS_URL"
)

type oidcDiscovery struct {
	Issuer  string `json:"issuer"`
	JWKSURL string `json:"jwks_uri"`
}

type jsonWebKeySet struct {
	Keys []jsonWebKey `json:"keys"`
}

type jsonWebKey struct {
	KID string `json:"kid"`
	KTY string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type oidcClaims struct {
	Issuer           string   `json:"iss"`
	Subject          string   `json:"sub"`
	Audience         any      `json:"aud"`
	Expires          int64    `json:"exp"`
	NotBefore        int64    `json:"nbf"`
	PreferredName    string   `json:"preferred_username"`
	Email            string   `json:"email"`
	Tenant           string   `json:"runeward_tenant"`
	Profiles         []string `json:"runeward_profiles"`
	ApprovalProfiles []string `json:"runeward_approval_profiles"`
	CanApprove       bool     `json:"runeward_can_approve"`
	Admin            bool     `json:"runeward_admin"`
}

// OIDCVerifier validates RS256 OIDC JWTs and maps signed Runeward claims onto
// the same Principal model used by local authz files.
type OIDCVerifier struct {
	issuer      string
	audience    string
	jwksURL     string
	client      *http.Client
	mu          sync.RWMutex
	keys        map[string]*rsa.PublicKey
	refreshMu   sync.Mutex
	lastRefresh time.Time
}

// NewOIDCFromEnv configures OIDC when RUNEWARD_OIDC_ISSUER is set.
func NewOIDCFromEnv() (*OIDCVerifier, bool, error) {
	issuer := strings.TrimRight(strings.TrimSpace(os.Getenv(EnvOIDCIssuer)), "/")
	if issuer == "" {
		return nil, false, nil
	}
	audience := strings.TrimSpace(os.Getenv(EnvOIDCAudience))
	if audience == "" {
		return nil, true, fmt.Errorf("%s is required when %s is configured", EnvOIDCAudience, EnvOIDCIssuer)
	}
	if err := requireSecureOIDCEndpoint(issuer); err != nil {
		return nil, true, err
	}
	v := &OIDCVerifier{issuer: issuer, audience: audience, jwksURL: strings.TrimSpace(os.Getenv(EnvOIDCJWKSURL)), client: &http.Client{Timeout: 10 * time.Second}}
	if v.jwksURL == "" {
		var discovery oidcDiscovery
		if err := v.getJSON(issuer+"/.well-known/openid-configuration", &discovery); err != nil {
			return nil, true, fmt.Errorf("discover OIDC provider: %w", err)
		}
		if discovery.Issuer != "" && strings.TrimRight(discovery.Issuer, "/") != issuer {
			return nil, true, errors.New("OIDC discovery issuer mismatch")
		}
		v.jwksURL = discovery.JWKSURL
	}
	if err := requireSecureOIDCEndpoint(v.jwksURL); err != nil {
		return nil, true, err
	}
	if v.jwksURL == "" {
		return nil, true, errors.New("OIDC provider did not advertise jwks_uri")
	}
	if err := v.refreshKeys(true); err != nil {
		return nil, true, err
	}
	return v, true, nil
}

func requireSecureOIDCEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("invalid OIDC endpoint %q", raw)
	}
	if u.Scheme == "https" {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1") {
		return nil
	}
	return errors.New("OIDC endpoints must use HTTPS (HTTP is allowed only for loopback development)")
}

func (v *OIDCVerifier) getJSON(url string, out any) error {
	resp, err := v.client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("%s returned HTTP %d", url, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out)
}

func (v *OIDCVerifier) refreshKeys(force bool) error {
	// An attacker can choose arbitrary kid values. Serialize and rate-limit
	// cache-miss refreshes so invalid bearer tokens cannot turn Runeward into a
	// request amplifier against the identity provider. Startup bypasses the
	// cooldown; normal key rotation is picked up after this short interval.
	v.refreshMu.Lock()
	defer v.refreshMu.Unlock()
	if !force && !v.lastRefresh.IsZero() && time.Since(v.lastRefresh) < 30*time.Second {
		return nil
	}
	var set jsonWebKeySet
	if err := v.getJSON(v.jwksURL, &set); err != nil {
		return fmt.Errorf("load OIDC keys: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey)
	for _, key := range set.Keys {
		if key.KTY != "RSA" || key.N == "" || key.E == "" ||
			(key.Use != "" && key.Use != "sig") || (key.Alg != "" && key.Alg != "RS256") {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(key.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(key.E)
		if err != nil || len(eBytes) == 0 || len(eBytes) > 4 {
			continue
		}
		e := 0
		for _, b := range eBytes {
			e = e<<8 | int(b)
		}
		if e <= 1 {
			continue
		}
		keys[key.KID] = &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}
	}
	if len(keys) == 0 {
		return errors.New("OIDC JWKS contains no usable RS256 keys")
	}
	v.mu.Lock()
	v.keys = keys
	v.lastRefresh = time.Now()
	v.mu.Unlock()
	return nil
}

func (v *OIDCVerifier) key(kid string) *rsa.PublicKey {
	v.mu.RLock()
	key := v.keys[kid]
	v.mu.RUnlock()
	return key
}

// Verify authenticates a JWT and derives a least-privilege Principal from its
// signed Runeward claims. Tokens without runeward_profiles can launch nothing.
func (v *OIDCVerifier) Verify(token string) (*Principal, error) {
	if len(token) > 32*1024 {
		return nil, errors.New("OIDC token exceeds maximum size")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("OIDC token is not a JWT")
	}
	var header struct {
		Algorithm string `json:"alg"`
		KID       string `json:"kid"`
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(headerJSON, &header) != nil || header.Algorithm != "RS256" || header.KID == "" {
		return nil, errors.New("unsupported OIDC token header")
	}
	key := v.key(header.KID)
	if key == nil {
		if err := v.refreshKeys(false); err != nil {
			return nil, err
		}
		key = v.key(header.KID)
	}
	if key == nil {
		return nil, errors.New("OIDC signing key not found")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("invalid OIDC signature encoding")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return nil, errors.New("invalid OIDC signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("invalid OIDC claims encoding")
	}
	var claims oidcClaims
	if json.Unmarshal(payload, &claims) != nil {
		return nil, errors.New("invalid OIDC claims")
	}
	now := time.Now().UTC().Unix()
	if strings.TrimRight(claims.Issuer, "/") != v.issuer || claims.Subject == "" || claims.Expires <= now || (claims.NotBefore != 0 && claims.NotBefore > now+30) || !audienceContains(claims.Audience, v.audience) {
		return nil, errors.New("OIDC claims validation failed")
	}
	name := firstClaim(claims.PreferredName, claims.Email, claims.Subject)
	tenant := firstClaim(claims.Tenant, claims.Subject)
	return &Principal{Name: name, Tenant: tenant, AllowedProfiles: claims.Profiles, ApprovalProfiles: claims.ApprovalProfiles, CanApprove: claims.CanApprove, Admin: claims.Admin}, nil
}

func firstClaim(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func audienceContains(raw any, want string) bool {
	switch value := raw.(type) {
	case string:
		return value == want
	case []any:
		for _, item := range value {
			if s, ok := item.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}
