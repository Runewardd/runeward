package controlplane

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adefemi171/runeward/internal/backend"
	"github.com/adefemi171/runeward/internal/browser"
	"github.com/adefemi171/runeward/internal/profile"
)

// browserDriverBin is the in-sandbox CDP driver (cmd/runeward-browser) that the
// default sandbox image ships on PATH alongside a Chromium binary.
const browserDriverBin = "runeward-browser"

// browserReadyTimeout bounds how long BrowserOpen waits for the driver's
// control socket to accept a ping after launch.
const browserReadyTimeout = 20 * time.Second

// browserSession tracks one live in-sandbox browser driver: its id and the
// control socket the driver listens on inside the sandbox.
type browserSession struct {
	id     string
	socket string
}

// BrowserOpen starts a stateful, CDP-driven browser session inside the sandbox
// and returns its session id. The driver (`runeward-browser serve`) is launched
// detached so this one-shot exec returns immediately; the profile's egress
// proxy is threaded through via --proxy so browser traffic stays governed. The
// open is gated by policy as tool "browser" (action "open") and audited; a deny
// or pending-approval verdict is returned in the ToolResult without starting a
// session.
func (m *Manager) BrowserOpen(ctx context.Context, id string) (sessionID string, res *ToolResult, err error) {
	sess, err := m.session(id)
	if err != nil {
		return "", nil, err
	}

	sid := randID()
	socket := fmt.Sprintf("/tmp/rw-browser-%s.sock", sid)
	proxy := sess.Env["HTTPS_PROXY"]
	if proxy == "" {
		proxy = sess.Env["HTTP_PROXY"]
	}
	proxyArg := ""
	if proxy != "" {
		proxyArg = " --proxy " + shQuote(proxy)
	}
	start := fmt.Sprintf(
		"command -v %s >/dev/null 2>&1 || { echo 'runeward-browser not found in sandbox image' >&2; exit 127; }; "+
			"setsid %s serve --socket %s%s >/tmp/rw-browser-%s.log 2>&1 & echo started",
		browserDriverBin, browserDriverBin, shQuote(socket), proxyArg, sid,
	)

	res, err = m.govern(ctx, sess, "browser", "open", []string{"open", sid}, func(ctx context.Context) (*backend.ExecResult, error) {
		return sess.Backend.Exec(ctx, id, backend.ExecRequest{Command: []string{"sh", "-c", start}, Workdir: sess.Workdir, Env: sess.Env})
	})
	if err != nil {
		return "", nil, err
	}
	if res.Verdict != profile.VerdictAllow {
		return "", res, nil
	}
	if res.ExitCode != 0 {
		return "", nil, fmt.Errorf("start browser driver: %s", strings.TrimSpace(res.Stderr+res.Stdout))
	}

	if err := m.browserWaitReady(ctx, sess, id, socket); err != nil {
		return "", nil, err
	}

	sess.browserMu.Lock()
	if sess.browsers == nil {
		sess.browsers = map[string]*browserSession{}
	}
	sess.browsers[sid] = &browserSession{id: sid, socket: socket}
	sess.browserMu.Unlock()

	return sid, res, nil
}

// BrowserAct sends one action to a live browser session and returns the
// governed result. The action runs through the full policy/guardrail/audit path
// as tool "browser". The driver's structured reply is unpacked into the
// ToolResult: Stdout carries the textual value (or base64 screenshot) and a
// driver-level failure surfaces in Reason.
func (m *Manager) BrowserAct(ctx context.Context, id, sessionID string, cmd browser.Command) (*ToolResult, error) {
	sess, err := m.session(id)
	if err != nil {
		return nil, err
	}
	bs, err := sess.browser(sessionID)
	if err != nil {
		return nil, err
	}
	if cmd.Action == "" {
		return nil, fmt.Errorf("action is required")
	}

	payload, err := json.Marshal(cmd)
	if err != nil {
		return nil, err
	}
	call := []string{browserDriverBin, "call", "--socket", bs.socket, "--json", string(payload)}

	arg := cmd.Action
	switch {
	case cmd.URL != "":
		arg = cmd.Action + " " + cmd.URL
	case cmd.Selector != "":
		arg = cmd.Action + " " + cmd.Selector
	}

	res, err := m.govern(ctx, sess, "browser", arg, call, func(ctx context.Context) (*backend.ExecResult, error) {
		return sess.Backend.Exec(ctx, id, backend.ExecRequest{Command: call, Workdir: sess.Workdir, Env: sess.Env})
	})
	if err != nil {
		return nil, err
	}
	if res.Verdict != profile.VerdictAllow {
		return res, nil
	}

	// Unpack the driver's Result. `call` exits non-zero on a driver-level
	// failure but still prints the Result JSON to stdout.
	var out browser.Result
	if e := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &out); e == nil {
		res.Stdout = out.Value
		if out.Screenshot != "" {
			res.Stdout = out.Screenshot
		}
		if !out.OK && out.Error != "" {
			res.Reason = out.Error
		}
	}
	return res, nil
}

// BrowserClose tells the driver to shut down (closing Chromium and removing its
// socket) and forgets the session. It is best-effort and always removes local
// bookkeeping.
func (m *Manager) BrowserClose(ctx context.Context, id, sessionID string) error {
	sess, err := m.session(id)
	if err != nil {
		return err
	}
	bs, err := sess.browser(sessionID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(browser.Command{Action: "close"})
	_, _ = sess.Backend.Exec(ctx, id, backend.ExecRequest{
		Command: []string{browserDriverBin, "call", "--socket", bs.socket, "--json", string(payload)},
		Workdir: sess.Workdir, Env: sess.Env,
	})
	sess.browserMu.Lock()
	delete(sess.browsers, sessionID)
	sess.browserMu.Unlock()
	m.record(sess, "browser", "close", []string{"close", sessionID}, string(profile.VerdictAllow), 0, 0, "")
	return nil
}

// browserWaitReady polls the driver's control socket with a ping until it
// answers or the readiness timeout elapses.
func (m *Manager) browserWaitReady(ctx context.Context, sess *Session, id, socket string) error {
	ping, _ := json.Marshal(browser.Command{Action: "ping"})
	deadline := time.Now().Add(browserReadyTimeout)
	call := []string{browserDriverBin, "call", "--socket", socket, "--json", string(ping)}
	for {
		res, err := sess.Backend.Exec(ctx, id, backend.ExecRequest{Command: call, Workdir: sess.Workdir, Env: sess.Env})
		if err == nil && res.ExitCode == 0 {
			var out browser.Result
			if json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &out) == nil && out.OK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("browser driver did not become ready within %s", browserReadyTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// browser returns the tracked browser session by id.
func (s *Session) browser(sessionID string) (*browserSession, error) {
	s.browserMu.Lock()
	defer s.browserMu.Unlock()
	bs, ok := s.browsers[sessionID]
	if !ok {
		return nil, fmt.Errorf("browser session %q not found", sessionID)
	}
	return bs, nil
}

// randID returns a short random hex id for a browser session.
func randID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
