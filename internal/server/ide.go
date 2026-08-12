package server

import (
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Runewardd/runeward/internal/authz"
	"github.com/Runewardd/runeward/internal/controlplane"
)

const (
	ideTicketTTL  = 30 * time.Second
	ideSessionTTL = 2 * time.Hour
	ideCookieName = "rw_ide_sess"
)

type ideCookieSession struct {
	SandboxID string
	Principal *authz.Principal
	ExpiresAt time.Time
}

// ideSessions maps cookie session ids to authenticated IDE browser sessions so
// code-server subresource loads work after the one-shot ticket is consumed.
type ideSessionStore struct {
	mu   sync.Mutex
	byID map[string]ideCookieSession
}

func (s *Server) handleIDETicket(w http.ResponseWriter, r *http.Request) {
	if !controlplane.ExperimentalIDEEnabled() {
		writeError(w, http.StatusNotFound, "experimental IDE is disabled; set RUNEWARD_ENABLE_EXPERIMENTAL_IDE=1")
		return
	}
	id := r.PathValue("id")
	if _, ok := s.mgr.Sandbox(id); !ok {
		writeError(w, http.StatusNotFound, "sandbox not found")
		return
	}
	if _, ok := s.mgr.IDEEndpoint(id); !ok {
		writeError(w, http.StatusNotFound, "ide not available for this citadel")
		return
	}
	p := principalFrom(r.Context())
	ticket, expiresAt, err := s.issueIDETicket(id, p, ideTicketTTL)
	if err != nil {
		writeServerError(w, s.logger, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"ticket":     ticket,
		"expires_at": expiresAt.UTC(),
	})
}

// handleIDE reverse-proxies HTTP and WebSocket traffic to the in-cell
// code-server after ticket/cookie/bearer auth (see authenticate).
func (s *Server) handleIDE(w http.ResponseWriter, r *http.Request) {
	if !controlplane.ExperimentalIDEEnabled() {
		writeError(w, http.StatusNotFound, "experimental IDE is disabled; set RUNEWARD_ENABLE_EXPERIMENTAL_IDE=1")
		return
	}
	id := r.PathValue("id")
	endpoint, ok := s.mgr.IDEEndpoint(id)
	if !ok {
		writeError(w, http.StatusNotFound, "ide not available for this citadel")
		return
	}

	prefix := "/v1/citadels/" + id + "/ide"
	// Prefer a trailing slash so relative asset URLs from code-server resolve.
	// Drop ?ticket= — it was already consumed in authenticate and replaced by
	// the IDE session cookie; replaying it on the redirect would 401.
	if r.URL.Path == prefix {
		// A static relative target preserves the current Citadel prefix without
		// reflecting path input into a redirect location.
		http.Redirect(w, r, "ide/", http.StatusTemporaryRedirect)
		return
	}

	target, err := parseIDEProxyTarget(endpoint)
	if err != nil {
		writeServerError(w, s.logger, err)
		return
	}

	// #nosec G704 -- parseIDEProxyTarget accepts only a numeric backend IP and
	// a bounded TCP port obtained from the authenticated in-memory Citadel session.
	proxy := httputil.NewSingleHostReverseProxy(target)
	// Required for WebSocket / long-lived streams through the proxy.
	proxy.FlushInterval = -1
	defaultDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		defaultDirector(req)
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		path := strings.TrimPrefix(req.URL.Path, prefix)
		if path == "" {
			path = "/"
		}
		req.URL.Path = path
		req.URL.RawPath = ""
		// Keep the browser-facing Host. Rewriting Host to the container IP makes
		// code-server/VS Code reject WebSocket upgrades (Origin vs Host → 403,
		// browser sees close 1006).
		req.Host = r.Host
		if req.Header.Get("X-Forwarded-Host") == "" {
			req.Header.Set("X-Forwarded-Host", r.Host)
		}
		if req.Header.Get("X-Forwarded-Proto") == "" {
			if r.TLS != nil {
				req.Header.Set("X-Forwarded-Proto", "https")
			} else {
				req.Header.Set("X-Forwarded-Proto", "http")
			}
		}
		req.Header.Set("X-Forwarded-Prefix", prefix)
		// Drop outer auth; code-server runs with --auth none on the container net.
		req.Header.Del("Authorization")
		stripCookie(req, ideCookieName)
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Do not touch 101 Switching Protocols — hijacked WebSocket responses.
		if resp.StatusCode == http.StatusSwitchingProtocols {
			return nil
		}
		if loc := resp.Header.Get("Location"); loc != "" {
			if u, err := url.Parse(loc); err == nil {
				if u.Host == "" && strings.HasPrefix(u.Path, "/") && !strings.HasPrefix(u.Path, prefix) {
					u.Path = prefix + u.Path
					resp.Header.Set("Location", u.String())
				}
			}
		}
		cookies := resp.Cookies()
		if len(cookies) > 0 {
			resp.Header.Del("Set-Cookie")
			for _, c := range cookies {
				if c.Path == "" || c.Path == "/" {
					c.Path = prefix + "/"
				}
				resp.Header.Add("Set-Cookie", c.String())
			}
		}
		return nil
	}
	proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, e error) {
		s.logger.Error("ide proxy error", "sandbox", id, "err", e)
		writeError(rw, http.StatusBadGateway, "ide upstream unavailable")
	}
	proxy.ServeHTTP(w, r)
}

func parseIDEProxyTarget(endpoint string) (*url.URL, error) {
	host, rawPort, err := net.SplitHostPort(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid ide endpoint: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
		return nil, fmt.Errorf("invalid ide endpoint IP %q", host)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid ide endpoint port %q", rawPort)
	}
	return &url.URL{Scheme: "http", Host: net.JoinHostPort(ip.String(), strconv.Itoa(port))}, nil
}

func stripCookie(req *http.Request, name string) {
	cookies := req.Cookies()
	req.Header.Del("Cookie")
	for _, c := range cookies {
		if c.Name == name {
			continue
		}
		req.AddCookie(c)
	}
}

func isIDEPath(path string) bool {
	const prefix = "/v1/citadels/"
	const marker = "/ide"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	rest := strings.TrimPrefix(path, prefix)
	i := strings.IndexByte(rest, '/')
	if i < 0 {
		return false
	}
	after := rest[i:]
	return after == marker || strings.HasPrefix(after, marker+"/")
}

func ideSandboxID(path string) (string, bool) {
	if !isIDEPath(path) {
		return "", false
	}
	rest := strings.TrimPrefix(path, "/v1/citadels/")
	i := strings.IndexByte(rest, '/')
	if i <= 0 {
		return "", false
	}
	id := rest[:i]
	return id, id != ""
}

func (s *Server) issueIDETicket(sandboxID string, p *authz.Principal, ttl time.Duration) (string, time.Time, error) {
	return s.issueTicket(ticketScope{Kind: ticketKindIDE, SandboxID: sandboxID}, p, ttl)
}

func (s *Server) attachIDESession(w http.ResponseWriter, sandboxID string, p *authz.Principal) {
	var raw [16]byte
	if _, err := crand.Read(raw[:]); err != nil {
		return
	}
	sid := hex.EncodeToString(raw[:])
	expires := time.Now().Add(ideSessionTTL)
	s.ideSessions.mu.Lock()
	if s.ideSessions.byID == nil {
		s.ideSessions.byID = make(map[string]ideCookieSession)
	}
	pruneExpiredIDESessions(s.ideSessions.byID, time.Now())
	s.ideSessions.byID[sid] = ideCookieSession{
		SandboxID: sandboxID,
		Principal: p,
		ExpiresAt: expires,
	}
	s.ideSessions.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     ideCookieName,
		Value:    sid,
		Path:     "/v1/citadels/" + sandboxID + "/ide",
		HttpOnly: true,
		// IDE sessions grant browser access to the sandbox, so never permit the
		// cookie to travel over a plaintext connection. Loopback development
		// origins are treated as potentially trustworthy by modern browsers;
		// remote serving already requires TLS or an explicitly trusted proxy.
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		Expires:  expires,
	})
}

func (s *Server) ideSessionFromCookie(r *http.Request, sandboxID string) (*authz.Principal, bool) {
	c, err := r.Cookie(ideCookieName)
	if err != nil || strings.TrimSpace(c.Value) == "" {
		return nil, false
	}
	s.ideSessions.mu.Lock()
	defer s.ideSessions.mu.Unlock()
	if s.ideSessions.byID == nil {
		return nil, false
	}
	pruneExpiredIDESessions(s.ideSessions.byID, time.Now())
	sess, ok := s.ideSessions.byID[c.Value]
	if !ok || time.Now().After(sess.ExpiresAt) {
		delete(s.ideSessions.byID, c.Value)
		return nil, false
	}
	if sess.SandboxID != sandboxID {
		return nil, false
	}
	return sess.Principal, true
}

func pruneExpiredIDESessions(sessions map[string]ideCookieSession, now time.Time) {
	for id, sess := range sessions {
		if !now.Before(sess.ExpiresAt) {
			delete(sessions, id)
		}
	}
}
