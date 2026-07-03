package controlplane

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/adefemi171/runeward/internal/backend"
	"github.com/adefemi171/runeward/internal/profile"
)

// ToolResult is the governed outcome of a single tool invocation returned to
// the REST/MCP layer.
type ToolResult struct {
	Verdict    profile.Verdict `json:"verdict"`
	Reason     string          `json:"reason,omitempty"`
	ApprovalID string          `json:"approval_id,omitempty"`
	Pending    bool            `json:"pending,omitempty"`
	ExitCode   int             `json:"exit_code"`
	Stdout     string          `json:"stdout,omitempty"`
	Stderr     string          `json:"stderr,omitempty"`
	DurationMS int64           `json:"duration_ms"`
}

// Shell runs a raw command vector in the sandbox under policy control.
func (m *Manager) Shell(ctx context.Context, id string, command []string, workdir string) (*ToolResult, error) {
	sess, err := m.session(id)
	if err != nil {
		return nil, err
	}
	if len(command) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	arg := strings.Join(command, " ")
	wd := workdir
	if wd == "" {
		wd = sess.Workdir
	}
	return m.govern(ctx, sess, "shell", arg, command, func(ctx context.Context) (*backend.ExecResult, error) {
		return sess.Backend.Exec(ctx, id, backend.ExecRequest{Command: command, Workdir: wd, Env: sess.Env})
	})
}

// Python runs a Python snippet via `python3 -c`.
func (m *Manager) Python(ctx context.Context, id, code string) (*ToolResult, error) {
	return m.runCode(ctx, id, "python", []string{"python3", "-c", code}, code)
}

// Node runs a JavaScript snippet via `node -e`.
func (m *Manager) Node(ctx context.Context, id, code string) (*ToolResult, error) {
	return m.runCode(ctx, id, "node", []string{"node", "-e", code}, code)
}

func (m *Manager) runCode(ctx context.Context, id, tool string, command []string, code string) (*ToolResult, error) {
	sess, err := m.session(id)
	if err != nil {
		return nil, err
	}
	return m.govern(ctx, sess, tool, code, nil, func(ctx context.Context) (*backend.ExecResult, error) {
		return sess.Backend.Exec(ctx, id, backend.ExecRequest{Command: command, Workdir: sess.Workdir, Env: sess.Env})
	})
}

// FileRead returns the contents of a file in the sandbox.
func (m *Manager) FileRead(ctx context.Context, id, path string) (*ToolResult, error) {
	sess, err := m.session(id)
	if err != nil {
		return nil, err
	}
	return m.govern(ctx, sess, "file.read", path, []string{path}, func(ctx context.Context) (*backend.ExecResult, error) {
		return sess.Backend.Exec(ctx, id, backend.ExecRequest{Command: []string{"cat", path}, Workdir: sess.Workdir, Env: sess.Env})
	})
}

// FileWrite writes content to a file in the sandbox, creating parent
// directories. Content is transported base64-encoded to stay binary-safe over
// the shell.
func (m *Manager) FileWrite(ctx context.Context, id, path, content string) (*ToolResult, error) {
	sess, err := m.session(id)
	if err != nil {
		return nil, err
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(content))
	script := fmt.Sprintf("mkdir -p \"$(dirname %s)\" && printf %%s '%s' | base64 -d > %s", shQuote(path), b64, shQuote(path))
	return m.govern(ctx, sess, "file.write", path, []string{path}, func(ctx context.Context) (*backend.ExecResult, error) {
		return sess.Backend.Exec(ctx, id, backend.ExecRequest{Command: []string{"sh", "-c", script}, Workdir: sess.Workdir, Env: sess.Env})
	})
}

// FileList lists a directory (long form) in the sandbox.
func (m *Manager) FileList(ctx context.Context, id, path string) (*ToolResult, error) {
	sess, err := m.session(id)
	if err != nil {
		return nil, err
	}
	if path == "" {
		path = "."
	}
	return m.govern(ctx, sess, "file.read", path, []string{path}, func(ctx context.Context) (*backend.ExecResult, error) {
		return sess.Backend.Exec(ctx, id, backend.ExecRequest{Command: []string{"ls", "-la", path}, Workdir: sess.Workdir, Env: sess.Env})
	})
}

// FileSearch runs a recursive text search (grep) rooted at path.
func (m *Manager) FileSearch(ctx context.Context, id, query, path string) (*ToolResult, error) {
	sess, err := m.session(id)
	if err != nil {
		return nil, err
	}
	if path == "" {
		path = "."
	}
	return m.govern(ctx, sess, "file.read", query, []string{query, path}, func(ctx context.Context) (*backend.ExecResult, error) {
		return sess.Backend.Exec(ctx, id, backend.ExecRequest{Command: []string{"grep", "-rn", query, path}, Workdir: sess.Workdir, Env: sess.Env})
	})
}

// Browser drives a headless Chromium inside the sandbox and returns the
// rendered page. It runs through the same governed path as every other tool
// (policy tool name "browser", arg = url), so URL allow/deny/approval rules and
// the audit ledger apply. Because it executes inside the sandbox, the profile's
// egress policy also constrains what the browser can reach: when an HTTP(S)
// proxy is configured (deny-by-default profiles), it is passed to Chromium via
// --proxy-server; strict L3 profiles enforce it transparently.
//
// mode is "text" (default; returns the rendered DOM HTML) or "screenshot"
// (returns a base64-encoded PNG in Stdout). The profile's image must provide a
// Chromium/Chrome binary.
func (m *Manager) Browser(ctx context.Context, id, url, mode string) (*ToolResult, error) {
	sess, err := m.session(id)
	if err != nil {
		return nil, err
	}
	if url == "" {
		return nil, fmt.Errorf("url is required")
	}
	script := browserScript(url, mode, sess.Env)
	return m.govern(ctx, sess, "browser", url, []string{url, mode}, func(ctx context.Context) (*backend.ExecResult, error) {
		return sess.Backend.Exec(ctx, id, backend.ExecRequest{Command: []string{"sh", "-c", script}, Workdir: sess.Workdir, Env: sess.Env})
	})
}

// browserScript builds the sh -c program that locates a Chromium binary and
// renders url. It threads any HTTP(S) proxy from the session env through
// --proxy-server so egress control also covers the browser.
func browserScript(url, mode string, env map[string]string) string {
	proxy := env["HTTPS_PROXY"]
	if proxy == "" {
		proxy = env["HTTP_PROXY"]
	}
	proxyArg := ""
	if proxy != "" {
		proxyArg = "--proxy-server=" + shQuote(proxy) + " "
	}
	// Common Chromium binary names across images.
	find := `CHROME=$(command -v chromium 2>/dev/null || command -v chromium-browser 2>/dev/null || command -v google-chrome 2>/dev/null || command -v google-chrome-stable 2>/dev/null || command -v headless-shell 2>/dev/null || echo chromium)`
	flags := `--headless=new --no-sandbox --disable-gpu --disable-dev-shm-usage --hide-scrollbars`
	if mode == "screenshot" {
		return find + `; "$CHROME" ` + flags + ` ` + proxyArg + `--screenshot=/tmp/rw-shot.png ` + shQuote(url) + ` >/dev/null 2>&1; base64 /tmp/rw-shot.png`
	}
	return find + `; "$CHROME" ` + flags + ` ` + proxyArg + `--dump-dom ` + shQuote(url)
}

// shQuote wraps s in single quotes, escaping embedded single quotes, for safe
// interpolation into an `sh -c` script.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
