package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adefemi171/runeward/internal/controlplane"
)

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	t.Setenv("RUNEWARD_STATE_DIR", t.TempDir())
	mgr, err := controlplane.New(t.TempDir())
	if err != nil {
		t.Fatalf("controlplane.New: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	return New(mgr, nil, nil).Handler()
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
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/audit/verify", nil))
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
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/approvals", nil))
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
	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", strings.NewReader(`{"profile":"does-not-exist"}`))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestUnknownSandbox404(t *testing.T) {
	h := newTestServer(t)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/sandboxes/nope", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}
