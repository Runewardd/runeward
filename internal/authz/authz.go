// Package authz provides multi-principal, RBAC-style access control for the
// control plane. It generalizes the legacy single static bearer token into a
// set of named principals, each with its own token, an allowed-profile glob
// list, and permission flags.
//
// The zero value and a nil *Store both mean "RBAC not configured", allowing the
// caller to fall back to legacy single-token behavior.
package authz

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"runtime"
	"strings"
	"sync"
)

// EnvFile is the environment variable that points at the RBAC principals file.
const EnvFile = "RUNEWARD_AUTHZ_FILE"

// Principal is a named identity that authenticates with a bearer token and
// carries its own authorization scope.
type Principal struct {
	// Name is the human-readable identity used as the audit actor.
	Name string `json:"name"`
	// Tenant is the ownership boundary shared by principals that may collaborate
	// on the same Citadels, Cohorts, snapshots, and runs. Empty preserves the
	// legacy one-principal-per-tenant behavior by falling back to Name.
	Tenant string `json:"tenant,omitempty"`
	// Token is the bearer token that authenticates this principal.
	Token string `json:"token"`
	// AllowedProfiles is a list of glob patterns (path.Match syntax) matched
	// against a profile name. An empty list for a non-admin principal means it
	// may launch nothing; a list containing "*" allows every profile.
	AllowedProfiles []string `json:"allowed_profiles"`
	// CanApprove permits the principal to approve or deny pending actions.
	CanApprove bool `json:"can_approve"`
	// ApprovalProfiles optionally limits approval visibility and decisions to
	// actions from matching profiles. Empty preserves global reviewer behavior.
	ApprovalProfiles []string `json:"approval_profiles,omitempty"`
	// Admin bypasses all profile restrictions and implies approval rights.
	Admin bool `json:"admin"`
}

// TenantID returns the stable resource-ownership boundary for the principal.
// Existing authz files remain isolated per principal until they explicitly
// assign multiple principals to the same tenant.
func (p *Principal) TenantID() string {
	if p == nil {
		return ""
	}
	if tenant := strings.TrimSpace(p.Tenant); tenant != "" {
		return tenant
	}
	return p.Name
}

// CanLaunch reports whether the principal may launch the named profile. Admins
// bypass all restrictions. For non-admins, the profile must match at least one
// of the AllowedProfiles glob patterns.
func (p *Principal) CanLaunch(profile string) bool {
	if p == nil {
		return false
	}
	if p.Admin {
		return true
	}
	for _, pattern := range p.AllowedProfiles {
		if pattern == "*" {
			return true
		}
		if ok, err := path.Match(pattern, profile); err == nil && ok {
			return true
		}
	}
	return false
}

// MayApprove reports whether the principal may approve or deny actions.
func (p *Principal) MayApprove() bool {
	if p == nil {
		return false
	}
	return p.Admin || p.CanApprove
}

// CanApproveProfile reports whether the principal may review an action from
// profileName. Admins may review everything. An approver with no explicit
// approval_profiles remains a global reviewer for backward compatibility.
func (p *Principal) CanApproveProfile(profileName string) bool {
	if p == nil || !p.MayApprove() {
		return false
	}
	if p.Admin || len(p.ApprovalProfiles) == 0 {
		return true
	}
	for _, pattern := range p.ApprovalProfiles {
		if pattern == "*" {
			return true
		}
		if ok, err := path.Match(pattern, profileName); err == nil && ok {
			return true
		}
	}
	return false
}

// MayLaunch reports whether the principal may launch any profile at all.
// Admins always may. Non-admins may when they have at least one non-empty
// allowed-profile pattern (including "*"); an empty list means they can
// launch nothing.
func (p *Principal) MayLaunch() bool {
	if p == nil {
		return false
	}
	if p.Admin {
		return true
	}
	for _, pattern := range p.AllowedProfiles {
		if strings.TrimSpace(pattern) != "" {
			return true
		}
	}
	return false
}

// Store holds principals indexed by token. It is safe for concurrent reads.
// A nil *Store means RBAC is not configured.
type Store struct {
	mu         sync.RWMutex
	byToken    map[string]*Principal
	principals []*Principal
	tokenHash  [][32]byte
	oidc       *OIDCVerifier
}

type storeFile struct {
	Principals []*Principal `json:"principals"`
}

// Load reads a JSON principals file of the form:
//
//	{"principals": [ {"name": "...", "token": "...", ...}, ... ]}
//
// It rejects entries with empty names, empty tokens, or duplicate tokens.
func Load(filePath string) (*Store, error) {
	if info, err := os.Stat(filePath); runtime.GOOS != "windows" && err == nil && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("authz: %s permissions %04o expose bearer tokens; use 0600", filePath, info.Mode().Perm())
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("authz: read %s: %w", filePath, err)
	}
	var sf storeFile
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sf); err != nil {
		return nil, fmt.Errorf("authz: parse %s: %w", filePath, err)
	}
	return newStore(sf.Principals)
}

func newStore(principals []*Principal) (*Store, error) {
	s := &Store{
		byToken:    make(map[string]*Principal, len(principals)),
		principals: make([]*Principal, 0, len(principals)),
		tokenHash:  make([][32]byte, 0, len(principals)),
	}
	byName := make(map[string]struct{}, len(principals))
	for i, p := range principals {
		if p == nil {
			return nil, fmt.Errorf("authz: principal at index %d is null", i)
		}
		p.Name = strings.TrimSpace(p.Name)
		p.Tenant = strings.TrimSpace(p.Tenant)
		if p.Name == "" {
			return nil, fmt.Errorf("authz: principal at index %d has empty name", i)
		}
		if _, dup := byName[p.Name]; dup {
			return nil, fmt.Errorf("authz: duplicate principal name %q", p.Name)
		}
		byName[p.Name] = struct{}{}
		if p.Token == "" {
			return nil, fmt.Errorf("authz: principal %q has empty token", p.Name)
		}
		if _, dup := s.byToken[p.Token]; dup {
			return nil, fmt.Errorf("authz: duplicate token for principal %q", p.Name)
		}
		s.byToken[p.Token] = p
		s.principals = append(s.principals, p)
		s.tokenHash = append(s.tokenHash, sha256.Sum256([]byte(p.Token)))
	}
	return s, nil
}

// FromEnv builds a Store from the file named by RUNEWARD_AUTHZ_FILE. When the
// variable is unset (or empty) it returns (nil, nil), signaling that RBAC is
// not configured and the caller should fall back to legacy single-token auth.
func FromEnv() (*Store, error) {
	filePath := strings.TrimSpace(os.Getenv(EnvFile))
	var store *Store
	var err error
	if filePath != "" {
		store, err = Load(filePath)
		if err != nil {
			return nil, err
		}
	} else {
		store, err = newStore(nil)
		if err != nil {
			return nil, err
		}
	}
	verifier, configured, err := NewOIDCFromEnv()
	if err != nil {
		return nil, err
	}
	if configured {
		store.oidc = verifier
	}
	if filePath == "" && !configured {
		return nil, nil
	}
	return store, nil
}

// Identify returns the principal that owns the given bearer token. It returns
// (nil, false) when the token is unknown, empty, or the store is nil. Token
// comparison is done with a constant-time compare to reduce timing leakage.
func (s *Store) Identify(token string) (*Principal, bool) {
	if s == nil || token == "" {
		return nil, false
	}
	s.mu.RLock()
	h := sha256.Sum256([]byte(token))
	var match *Principal
	for i, p := range s.principals {
		if subtle.ConstantTimeCompare(s.tokenHash[i][:], h[:]) == 1 {
			match = p
		}
	}
	oidc := s.oidc
	s.mu.RUnlock()
	if match != nil {
		return match, true
	}
	if oidc != nil {
		p, err := oidc.Verify(token)
		if err == nil {
			return p, true
		}
	}
	return nil, false
}

// Len reports the number of configured principals. A nil Store has length 0.
func (s *Store) Len() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.principals)
}

// ValidateForNetwork rejects credentials that are too short for an exposed
// control plane. Local development may keep compact fixtures, while any
// non-loopback listener requires at least 32 bytes of token entropy material.
func (s *Store) ValidateForNetwork() error {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.principals {
		if len(p.Token) < 32 {
			return fmt.Errorf("authz: principal %q token is too short for network use; require at least 32 characters", p.Name)
		}
	}
	return nil
}

// ErrEmpty is returned by helpers that require a configured store. It is
// exported for callers that wish to distinguish "not configured" from other
// failures.
var ErrEmpty = errors.New("authz: store not configured")
