package controlplane

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Runewardd/runeward/internal/backend"
	"github.com/Runewardd/runeward/internal/profile"
)

const (
	ideBinary            = "code-server"
	ideReadyTimeout      = 45 * time.Second
	ideReadyPollInterval = 250 * time.Millisecond
)

func experimentalIDEEnabled() bool {
	return strings.TrimSpace(os.Getenv("RUNEWARD_ENABLE_EXPERIMENTAL_IDE")) == "1"
}

// ExperimentalIDEEnabled reports whether the browser IDE feature flag is on.
func ExperimentalIDEEnabled() bool { return experimentalIDEEnabled() }

// startIDE launches code-server inside the Citadel and records its bridge/pod
// endpoint for the ticketed reverse proxy. No-op when the Charter has IDE
// disabled or the experimental flag is off.
func (m *Manager) startIDE(ctx context.Context, sess *Session) error {
	if sess == nil || sess.Profile == nil || !sess.Profile.IDE.Enabled {
		return nil
	}
	if !experimentalIDEEnabled() {
		return nil
	}

	port := sess.Profile.IDE.Port
	if port <= 0 {
		port = 8080
	}
	workdir := sess.Workdir
	if workdir == "" {
		workdir = "/workspace"
	}

	start := fmt.Sprintf(
		"command -v %s >/dev/null 2>&1 || { echo '%s not found in Citadel image; build deploy/Dockerfile.ide' >&2; exit 127; }; "+
			"setsid %s --bind-addr 0.0.0.0:%d --auth none --disable-telemetry --disable-update-check %s "+
			">/tmp/rw-ide.log 2>&1 & echo started",
		ideBinary, ideBinary, ideBinary, port, shQuote(workdir),
	)

	res, err := sess.Backend.Exec(ctx, sess.Sandbox.ID, backend.ExecRequest{
		Command: []string{"sh", "-c", start},
		Workdir: workdir,
		Env:     sess.Env,
	})
	if err != nil {
		return fmt.Errorf("start ide: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("start ide: %s", strings.TrimSpace(res.Stderr+res.Stdout))
	}

	reachable, ok := sess.Backend.(backend.Reachable)
	if !ok {
		return fmt.Errorf("backend %q cannot resolve container IP for IDE proxy", sess.Backend.Name())
	}
	ip, err := reachable.ContainerIP(ctx, sess.Sandbox.ID)
	if err != nil {
		return fmt.Errorf("resolve ide endpoint: %w", err)
	}
	endpoint := net.JoinHostPort(ip, strconv.Itoa(port))
	if err := waitIDEReady(ctx, endpoint); err != nil {
		return err
	}

	sess.ideMu.Lock()
	sess.ideEndpoint = endpoint
	sess.ideMu.Unlock()

	m.record(ctx, sess, "ide", "open", []string{endpoint}, string(profile.VerdictAllow), 0, 0, "")
	return nil
}

func waitIDEReady(ctx context.Context, endpoint string) error {
	deadline := time.Now().Add(ideReadyTimeout)
	client := &http.Client{Timeout: 2 * time.Second}
	url := "http://" + endpoint + "/"
	var last error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			// code-server may 302 to login or return 200; any HTTP response means up.
			return nil
		}
		last = err
		time.Sleep(ideReadyPollInterval)
	}
	if last == nil {
		last = fmt.Errorf("no response")
	}
	return fmt.Errorf("ide not ready at %s: %w", endpoint, last)
}

// IDEEndpoint returns the in-cell IDE host:port when the experimental IDE is
// running for the Citadel.
func (m *Manager) IDEEndpoint(id string) (string, bool) {
	sess, err := m.session(id)
	if err != nil {
		return "", false
	}
	sess.ideMu.Lock()
	defer sess.ideMu.Unlock()
	ep := sess.ideEndpoint
	return ep, ep != ""
}

// IDEAgents returns Charter-declared CLI agent hints for the Citadel IDE UI.
func (m *Manager) IDEAgents(id string) []string {
	sess, err := m.session(id)
	if err != nil || sess.Profile == nil {
		return nil
	}
	out := make([]string, len(sess.Profile.IDE.Agents))
	copy(out, sess.Profile.IDE.Agents)
	return out
}

// recordIDEClose appends an ide.close Chronicle event when an IDE session was
// started for the Citadel.
func (m *Manager) recordIDEClose(ctx context.Context, sess *Session) {
	if sess == nil {
		return
	}
	sess.ideMu.Lock()
	ep := sess.ideEndpoint
	sess.ideMu.Unlock()
	if ep == "" {
		return
	}
	m.record(ctx, sess, "ide", "close", []string{ep}, string(profile.VerdictAllow), 0, 0, "")
}

// InjectSession registers a session for tests (no backend create).
func (m *Manager) InjectSession(sess *Session) {
	if sess == nil || sess.Sandbox == nil || sess.Sandbox.ID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions == nil {
		m.sessions = make(map[string]*Session)
	}
	m.sessions[sess.Sandbox.ID] = sess
}

// SetIDEEndpointForTest sets the IDE proxy target without starting code-server.
func (m *Manager) SetIDEEndpointForTest(id, endpoint string) {
	sess, err := m.session(id)
	if err != nil {
		return
	}
	sess.ideMu.Lock()
	sess.ideEndpoint = endpoint
	sess.ideMu.Unlock()
}
