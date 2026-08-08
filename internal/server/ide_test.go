package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Runewardd/runeward/internal/authz"
	"github.com/Runewardd/runeward/internal/backend"
	"github.com/Runewardd/runeward/internal/controlplane"
	"github.com/Runewardd/runeward/internal/profile"
	"github.com/gorilla/websocket"
)

func newTestServerFull(t *testing.T, token string) (*Server, http.Handler) {
	t.Helper()
	t.Setenv("RUNEWARD_STATE_DIR", t.TempDir())
	mgr, err := controlplane.New(t.TempDir())
	if err != nil {
		t.Fatalf("controlplane.New: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	srv := New(mgr, nil, nil)
	srv.AuthToken = token
	return srv, srv.Handler()
}

func injectIDESession(t *testing.T, srv *Server, id, endpoint, owner string) {
	t.Helper()
	srv.mgr.InjectSession(&controlplane.Session{
		Sandbox: &backend.Sandbox{ID: id, Profile: "ide-demo", Backend: "fake", Status: "running"},
		Profile: &profile.Profile{Name: "ide-demo", IDE: profile.IDE{Enabled: true, Port: 8080}},
		Owner:   owner,
	})
	srv.mgr.SetIDEEndpointForTest(id, endpoint)
}

func TestIDETicketSingleUseAndProxy(t *testing.T) {
	t.Setenv("RUNEWARD_ENABLE_EXPERIMENTAL_IDE", "1")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "ide-ok")
	}))
	t.Cleanup(upstream.Close)
	host := strings.TrimPrefix(upstream.URL, "http://")

	srv, h := newTestServerFull(t, "s3cret")
	injectIDESession(t, srv, "ide1", host, "")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/tickets", strings.NewReader(`{"kind":"ide","sandbox_id":"ide1"}`))
	req.Header.Set("Authorization", "Bearer s3cret")
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("mint status = %d, want 201: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(bytes.NewReader(rr.Body.Bytes())).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ticket, _ := body["ticket"].(string)
	if ticket == "" {
		t.Fatal("empty ticket")
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/citadels/ide1/ide/?ticket="+ticket, nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("proxy status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if got := rr.Body.String(); !strings.Contains(got, "ide-ok") {
		t.Fatalf("body = %q, want ide-ok", got)
	}
	cookie := rr.Result().Header.Get("Set-Cookie")
	if !strings.Contains(cookie, ideCookieName+"=") {
		t.Fatalf("expected IDE session cookie, got %q", cookie)
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/citadels/ide1/ide/?ticket="+ticket, nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("reuse status = %d, want 401", rr.Code)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/citadels/ide1/ide/", nil)
	req.Header.Set("Cookie", cookie)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("cookie status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
}

func TestIDEProxyRedirectAndSecureCookie(t *testing.T) {
	t.Setenv("RUNEWARD_ENABLE_EXPERIMENTAL_IDE", "1")
	srv, h := newTestServerFull(t, "s3cret")
	injectIDESession(t, srv, "ide1", "127.0.0.1:8080", "")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://runeward.example/v1/citadels/ide1/ide", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	h.ServeHTTP(rr, req)
	const wantLocation = "/v1/citadels/ide1/ide/"
	if rr.Code != http.StatusTemporaryRedirect || rr.Header().Get("Location") != wantLocation {
		t.Fatalf("redirect = %d %q, want 307 %s", rr.Code, rr.Header().Get("Location"), wantLocation)
	}

	rr = httptest.NewRecorder()
	// TLS keeps the session cookie Secure.
	srv.attachIDESession(rr, httptest.NewRequest(http.MethodGet, "https://runeward.example/v1/citadels/ide1/ide/", nil), "ide1", nil)
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("IDE cookie is not hardened: %#v", cookies)
	}

	// Loopback HTTP must omit Secure or browsers reject the cookie and every
	// code-server asset/WebSocket request becomes unauthorized.
	rr = httptest.NewRecorder()
	srv.attachIDESession(rr, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/v1/citadels/ide1/ide/", nil), "ide1", nil)
	cookies = rr.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("loopback IDE cookie is not locally usable and hardened: %#v", cookies)
	}

	rr = httptest.NewRecorder()
	proxied := httptest.NewRequest(http.MethodGet, "http://runeward.internal/v1/citadels/ide1/ide/", nil)
	proxied.Header.Set("X-Forwarded-Proto", "https")
	srv.attachIDESession(rr, proxied, "ide1", nil)
	if cookies = rr.Result().Cookies(); len(cookies) != 1 || !cookies[0].Secure {
		t.Fatalf("forwarded HTTPS IDE cookie must remain Secure: %#v", cookies)
	}
}

func TestParseIDEProxyTarget(t *testing.T) {
	for _, endpoint := range []string{"127.0.0.1:8080", "[::1]:8080", "10.0.0.5:65535"} {
		if _, err := parseIDEProxyTarget(endpoint); err != nil {
			t.Fatalf("%s: %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{"example.com:8080", "127.0.0.1", "0.0.0.0:8080", "127.0.0.1:65536"} {
		if _, err := parseIDEProxyTarget(endpoint); err == nil {
			t.Fatalf("expected %s to be rejected", endpoint)
		}
	}
}

func TestIDEDisabledWithoutFlag(t *testing.T) {
	t.Setenv("RUNEWARD_ENABLE_EXPERIMENTAL_IDE", "")
	srv, h := newTestServerFull(t, "s3cret")
	injectIDESession(t, srv, "ide1", "127.0.0.1:9", "")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/citadels/ide1/ide-ticket", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestIDEProxyRejectsForeignOwner(t *testing.T) {
	t.Setenv("RUNEWARD_ENABLE_EXPERIMENTAL_IDE", "1")
	t.Setenv("RUNEWARD_STATE_DIR", t.TempDir())
	mgr, err := controlplane.New(t.TempDir())
	if err != nil {
		t.Fatalf("controlplane.New: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	p := filepath.Join(t.TempDir(), "authz.json")
	cfg := `{
		"principals":[
			{"name":"alice","token":"tok-alice","allowed_profiles":["*"]},
			{"name":"bob","token":"tok-bob","allowed_profiles":["*"]}
		]
	}`
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := authz.Load(p)
	if err != nil {
		t.Fatal(err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "secret")
	}))
	t.Cleanup(upstream.Close)

	srv := New(mgr, nil, nil)
	srv.Authz = store
	mgr.InjectSession(&controlplane.Session{
		Sandbox: &backend.Sandbox{ID: "ide-alice", Profile: "ide-demo", Status: "running"},
		Profile: &profile.Profile{Name: "ide-demo", IDE: profile.IDE{Enabled: true, Port: 8080}},
		Owner:   "alice",
	})
	mgr.SetIDEEndpointForTest("ide-alice", strings.TrimPrefix(upstream.URL, "http://"))
	h := srv.Handler()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/tickets", strings.NewReader(`{"kind":"ide","sandbox_id":"ide-alice"}`))
	req.Header.Set("Authorization", "Bearer tok-bob")
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("mint status = %d, want 404", rr.Code)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/tickets", strings.NewReader(`{"kind":"ide","sandbox_id":"ide-alice"}`))
	req.Header.Set("Authorization", "Bearer tok-alice")
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("alice mint = %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	_ = json.NewDecoder(bytes.NewReader(rr.Body.Bytes())).Decode(&body)
	ticket, _ := body["ticket"].(string)

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/citadels/ide-alice/ide/?ticket="+ticket, nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("alice ticket use = %d: %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/citadels/ide-alice/ide/", nil)
	req.Header.Set("Authorization", "Bearer tok-bob")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("bob access = %d, want 404", rr.Code)
	}
}

func TestIDEWebSocketUpgrade(t *testing.T) {
	t.Setenv("RUNEWARD_ENABLE_EXPERIMENTAL_IDE", "1")

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteMessage(websocket.TextMessage, []byte("pong"))
	}))
	t.Cleanup(upstream.Close)

	srv, h := newTestServerFull(t, "s3cret")
	injectIDESession(t, srv, "ide-ws", strings.TrimPrefix(upstream.URL, "http://"), "")

	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/tickets", strings.NewReader(`{"kind":"ide","sandbox_id":"ide-ws"}`))
	req.Header.Set("Authorization", "Bearer s3cret")
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	var body map[string]any
	_ = json.NewDecoder(bytes.NewReader(rr.Body.Bytes())).Decode(&body)
	ticket, _ := body["ticket"].(string)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/citadels/ide-ws/ide/?ticket=" + ticket
	hdr := http.Header{}
	hdr.Set("Origin", "http://"+strings.TrimPrefix(ts.URL, "http://"))
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial: %v (status=%d)", err, status)
	}
	defer conn.Close()
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(msg) != "pong" {
		t.Fatalf("msg = %q, want pong", msg)
	}
}
