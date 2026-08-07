package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Runewardd/runeward/internal/authz"
	"github.com/Runewardd/runeward/internal/controlplane"
)

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	return newTestServerWithToken(t, "")
}

func newTestServerWithToken(t *testing.T, token string) http.Handler {
	t.Helper()
	t.Setenv("RUNEWARD_STATE_DIR", t.TempDir())
	mgr, err := controlplane.New(t.TempDir())
	if err != nil {
		t.Fatalf("controlplane.New: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	srv := New(mgr, nil, nil)
	srv.AuthToken = token
	return srv.Handler()
}

func newTestServerWithRBAC(t *testing.T) http.Handler {
	t.Helper()
	t.Setenv("RUNEWARD_STATE_DIR", t.TempDir())
	mgr, err := controlplane.New(t.TempDir())
	if err != nil {
		t.Fatalf("controlplane.New: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	p := filepath.Join(t.TempDir(), "authz.json")
	cfg := `{
		"principals":[
			{"name":"admin","token":"tok-admin","admin":true},
			{"name":"alice","tenant":"team-alpha","token":"tok-alice","allowed_profiles":["team-*"]},
			{"name":"bob","tenant":"team-alpha","token":"tok-bob","allowed_profiles":["team-*"]},
			{"name":"eve","tenant":"team-other","token":"tok-eve","allowed_profiles":["team-*"]},
			{"name":"reviewer","tenant":"team-alpha","token":"tok-reviewer","can_approve":true,"allowed_profiles":["team-*"]}
		]
	}`
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write authz config: %v", err)
	}
	store, err := authz.Load(p)
	if err != nil {
		t.Fatalf("authz.Load: %v", err)
	}
	srv := New(mgr, nil, nil)
	srv.Authz = store
	return srv.Handler()
}

func TestAuthTokenRequired(t *testing.T) {
	h := newTestServerWithToken(t, "s3cret")

	// No token: rejected.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/citadels", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", rr.Code)
	}

	// Wrong token: rejected.
	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/citadels", nil)
	req.Header.Set("Authorization", "Bearer nope")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want 401", rr.Code)
	}

	// Correct bearer token: allowed.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/citadels", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("valid token: status = %d, want 200", rr.Code)
	}

	// Query-param token is no longer accepted for normal REST requests.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/citadels?token=s3cret", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("query token on REST: status = %d, want 401", rr.Code)
	}

	// Long-lived query-param tokens are rejected for terminal WebSockets too;
	// browser clients use short-lived scoped tickets.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/citadels/nope/terminal?token=s3cret", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("query token on terminal: status = %d, want 401", rr.Code)
	}

	// /healthz is always exempt.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("healthz: status = %d, want 200", rr.Code)
	}
}

func TestHealth(t *testing.T) {
	h := newTestServer(t)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status field = %q", body["status"])
	}
}

func TestAuditVerifyEmpty(t *testing.T) {
	h := newTestServer(t)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/chronicle/verify", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if ok, _ := body["ok"].(bool); !ok {
		t.Fatalf("empty ledger should verify ok, got %v", body)
	}
}

func TestApprovalsEmpty(t *testing.T) {
	h := newTestServer(t)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/conclave", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"approvals":[]`) {
		t.Fatalf("expected empty approvals array, got %s", rr.Body.String())
	}
}

func TestCreateSandboxUnknownProfile(t *testing.T) {
	h := newTestServer(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/citadels", strings.NewReader(`{"profile":"does-not-exist"}`))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestUnknownSandbox404(t *testing.T) {
	h := newTestServer(t)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/citadels/nope", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestRBACApprovalsInboxRequiresApprovalPermission(t *testing.T) {
	h := newTestServerWithRBAC(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/conclave", nil)
	req.Header.Set("Authorization", "Bearer tok-alice")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestRBACReadinessRequiresCharterScope(t *testing.T) {
	h := newTestServerWithRBAC(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/readiness?profile=ops-prod", nil)
	req.Header.Set("Authorization", "Bearer tok-alice")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestRBACCopyFromRequiresAdmin(t *testing.T) {
	h := newTestServerWithRBAC(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/citadels", strings.NewReader(`{"profile":"team-dev","copy_from":"/etc"}`))
	req.Header.Set("Authorization", "Bearer tok-alice")
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rr.Code, rr.Body.String())
	}
}

func TestStructuredAuthorizationError(t *testing.T) {
	h := newTestServerWithRBAC(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/conclave", nil)
	req.Header.Set("Authorization", "Bearer tok-alice")
	h.ServeHTTP(rr, req)
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != "authz_denied" {
		t.Fatalf("code = %v, want authz_denied", body["code"])
	}
}

func TestRBACAuditExportRequiresAdmin(t *testing.T) {
	h := newTestServerWithRBAC(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/chronicle/export", nil)
	req.Header.Set("Authorization", "Bearer tok-reviewer")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestCreateFleetEnforcesCanLaunch(t *testing.T) {
	h := newTestServerWithRBAC(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/cohorts", strings.NewReader(`{"profile":"ops-prod"}`))
	req.Header.Set("Authorization", "Bearer tok-alice")
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestWhoamiCanLaunchReflectsAllowedProfiles(t *testing.T) {
	h := newTestServerWithRBAC(t)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer tok-alice")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("alice status = %d, want 200", rr.Code)
	}
	var alice map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &alice); err != nil {
		t.Fatalf("decode alice: %v", err)
	}
	p, _ := alice["principal"].(map[string]any)
	if p["can_launch"] != true {
		t.Fatalf("alice can_launch = %v, want true", p["can_launch"])
	}
	if p["tenant"] != "team-alpha" {
		t.Fatalf("alice tenant = %v, want team-alpha", p["tenant"])
	}

	// Add a locked principal via a fresh store with empty allowed_profiles.
	t.Setenv("RUNEWARD_STATE_DIR", t.TempDir())
	mgr, err := controlplane.New(t.TempDir())
	if err != nil {
		t.Fatalf("controlplane.New: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	cfgPath := filepath.Join(t.TempDir(), "authz.json")
	if err := os.WriteFile(cfgPath, []byte(`{"principals":[{"name":"locked","token":"tok-locked","allowed_profiles":[]}]}`), 0o600); err != nil {
		t.Fatalf("write authz: %v", err)
	}
	store, err := authz.Load(cfgPath)
	if err != nil {
		t.Fatalf("authz.Load: %v", err)
	}
	srv := New(mgr, nil, nil)
	srv.Authz = store
	h2 := srv.Handler()

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer tok-locked")
	h2.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("locked status = %d, want 200", rr.Code)
	}
	var locked map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &locked); err != nil {
		t.Fatalf("decode locked: %v", err)
	}
	lp, _ := locked["principal"].(map[string]any)
	if lp["can_launch"] != false {
		t.Fatalf("locked can_launch = %v, want false", lp["can_launch"])
	}
}

func TestRBACTenantAllowsCollaborationButBlocksOtherTenant(t *testing.T) {
	state := t.TempDir()
	t.Setenv("RUNEWARD_STATE_DIR", state)
	if err := os.WriteFile(filepath.Join(state, "fleets.json"), []byte(`[{
		"id":"fleet-team","profile":"team-dev","owner":"team-alpha",
		"sandboxes":[],"created":"2026-01-01T00:00:00Z","tasks":[]
	}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr, err := controlplane.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	authFile := filepath.Join(t.TempDir(), "authz.json")
	if err := os.WriteFile(authFile, []byte(`{"principals":[
		{"name":"alice","tenant":"team-alpha","token":"tok-alice"},
		{"name":"bob","tenant":"team-alpha","token":"tok-bob"},
		{"name":"eve","tenant":"team-other","token":"tok-eve"}
	]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := authz.Load(authFile)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(mgr, nil, nil)
	srv.Authz = store
	h := srv.Handler()
	for _, tc := range []struct {
		token string
		want  int
	}{{"tok-alice", http.StatusOK}, {"tok-bob", http.StatusOK}, {"tok-eve", http.StatusNotFound}} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/cohorts/fleet-team", nil)
		req.Header.Set("Authorization", "Bearer "+tc.token)
		h.ServeHTTP(rr, req)
		if rr.Code != tc.want {
			t.Fatalf("token %s: status=%d want=%d body=%s", tc.token, rr.Code, tc.want, rr.Body.String())
		}
	}
}

func TestRBACChartersFilteredByCanLaunch(t *testing.T) {
	h := newTestServerWithRBAC(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/charters", nil)
	req.Header.Set("Authorization", "Bearer tok-alice")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Profiles []struct {
			Name string `json:"name"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, p := range body.Profiles {
		if !strings.HasPrefix(p.Name, "team-") {
			t.Fatalf("alice saw unauthorized charter %q", p.Name)
		}
	}
}

func TestRBACPolicySimulateRequiresCanLaunch(t *testing.T) {
	h := newTestServerWithRBAC(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/policy/simulate", strings.NewReader(`{
		"profile_name":"ops-prod",
		"actions":[{"tool":"shell","command":"echo hi"}]
	}`))
	req.Header.Set("Authorization", "Bearer tok-alice")
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/policy/simulate", strings.NewReader(`{
		"profile":{"host":{"type":"container","image":"debian:stable-slim"},"policy":[]},
		"actions":[{"tool":"shell","command":"echo hi"}]
	}`))
	req.Header.Set("Authorization", "Bearer tok-alice")
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("inline status = %d, want 403: %s", rr.Code, rr.Body.String())
	}
}

func TestTaskOwnerFromRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/cohorts/f/tasks/t/complete", nil)
	if _, err := taskOwnerFromRequest(req, ""); err == nil {
		t.Fatalf("expected error for empty unauthenticated owner")
	}
	if owner, err := taskOwnerFromRequest(req, "worker-a"); err != nil || owner != "worker-a" {
		t.Fatalf("owner = %q, err = %v, want worker-a,nil", owner, err)
	}
	p := &authz.Principal{Name: "alice"}
	ctx := context.WithValue(req.Context(), principalCtxKey{}, p)
	req = req.WithContext(ctx)
	if owner, err := taskOwnerFromRequest(req, "worker-a"); err != nil || owner != "alice" {
		t.Fatalf("owner = %q, err = %v, want alice,nil", owner, err)
	}
}

func TestTerminalTicketSingleUse(t *testing.T) {
	h := newTestServerWithToken(t, "s3cret")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/citadels/sb1/terminal-ticket", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("mint status = %d, want 201", rr.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(bytes.NewReader(rr.Body.Bytes())).Decode(&body); err != nil {
		t.Fatalf("decode mint response: %v", err)
	}
	ticket, _ := body["ticket"].(string)
	if strings.TrimSpace(ticket) == "" {
		t.Fatalf("ticket was empty")
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/citadels/sb1/terminal?ticket="+ticket, nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("first use status = %d, want 404", rr.Code)
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/citadels/sb1/terminal?ticket="+ticket, nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("second use status = %d, want 401", rr.Code)
	}
}

func TestGeneralDownloadTicketSingleUse(t *testing.T) {
	h := newTestServerWithToken(t, "s3cret")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/tickets", strings.NewReader(`{"kind":"download","path":"/v1/chronicle/export"}`))
	req.Header.Set("Authorization", "Bearer s3cret")
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("mint status = %d, want 201", rr.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(bytes.NewReader(rr.Body.Bytes())).Decode(&body); err != nil {
		t.Fatalf("decode mint response: %v", err)
	}
	ticket, _ := body["ticket"].(string)
	if strings.TrimSpace(ticket) == "" {
		t.Fatalf("ticket was empty")
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/chronicle/export?ticket="+ticket, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("first use status = %d, want 200", rr.Code)
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/chronicle/export?ticket="+ticket, nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("second use status = %d, want 401", rr.Code)
	}
}

func TestPolicySimulateBuiltin(t *testing.T) {
	h := newTestServer(t)
	body := `{
		"profile": {
			"host": {"type":"container","image":"ghcr.io/runewardd/runeward-sandbox:latest","workdir":"/workspace"},
			"network": {"default":"allow"},
			"policy":[
				{"tool":"shell","match":"rm *","verdict":"deny","reason":"dangerous"},
				{"tool":"*","match":"*","verdict":"allow"}
			]
		},
		"actions":[
			{"name":"deny rm","tool":"shell","command":"rm -rf /"},
			{"name":"allow ls","tool":"shell","command":"ls -la"}
		]
	}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/policy/simulate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Results []struct {
			Name    string `json:"name"`
			Verdict string `json:"verdict"`
			Trace   []any  `json:"trace"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("results len = %d, want 2", len(resp.Results))
	}
	if resp.Results[0].Verdict != "deny" {
		t.Fatalf("first verdict = %q, want deny", resp.Results[0].Verdict)
	}
	if len(resp.Results[0].Trace) == 0 {
		t.Fatalf("expected trace entries")
	}
	if resp.Results[1].Verdict != "allow" {
		t.Fatalf("second verdict = %q, want allow", resp.Results[1].Verdict)
	}
}
