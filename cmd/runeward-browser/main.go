// Command runeward-browser is the in-sandbox stateful browser driver, driven
// over CDP.
//
// Usage:
//
//	runeward-browser serve --socket <path> [--proxy <url>]
//	runeward-browser call  --socket <path> [--json '<Command JSON>']
//
// `serve` runs a persistent headless Chromium behind a Unix socket; each
// connection carries one JSON [browser.Command] and one [browser.Result]. The
// page is kept alive across connections, so cookies and storage persist for
// the whole session. `call` sends one Command (from --json or stdin), prints
// the Result, and exits non-zero if Result.OK is false.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/adefemi171/runeward/internal/browser"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		runServe(os.Args[2:])
	case "call":
		runCall(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "runeward-browser: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `runeward-browser — in-sandbox CDP browser driver

Usage:
  runeward-browser serve --socket <path> [--proxy <url>]
  runeward-browser call  --socket <path> [--json '<Command JSON>']
`)
}

// chromeNames mirrors the binary search in the legacy one-shot browser tool
// (internal/controlplane/executor.go).
var chromeNames = []string{
	"chromium",
	"chromium-browser",
	"google-chrome",
	"google-chrome-stable",
	"headless-shell",
}

func findChrome() (string, error) {
	for _, name := range chromeNames {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no chromium binary found (looked for %s)", strings.Join(chromeNames, ", "))
}

// runServe launches Chromium, attaches CDP, and serves the control socket.
func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	socket := fs.String("socket", "", "path to the Unix domain control socket to listen on")
	proxy := fs.String("proxy", "", "HTTP(S) proxy passed to Chromium via --proxy-server")
	_ = fs.Parse(args)

	if *socket == "" {
		fmt.Fprintln(os.Stderr, "serve: --socket is required")
		os.Exit(2)
	}

	logger := logf("runeward-browser: ")

	chrome, err := findChrome()
	if err != nil {
		logger("%v", err)
		os.Exit(1)
	}

	udd, err := os.MkdirTemp("", "runeward-browser-")
	if err != nil {
		logger("create user-data-dir: %v", err)
		os.Exit(1)
	}

	chromeArgs := []string{
		"--headless=new",
		"--no-sandbox",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--hide-scrollbars",
		"--remote-debugging-port=0",
		"--user-data-dir=" + udd,
	}
	if *proxy != "" {
		chromeArgs = append(chromeArgs, "--proxy-server="+*proxy)
	}

	cmd := exec.Command(chrome, chromeArgs...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(udd)
		logger("launch chromium: %v", err)
		os.Exit(1)
	}

	d := &driver{
		chrome: cmd,
		udd:    udd,
		socket: *socket,
		logger: logger,
	}

	port, err := waitForDevToolsPort(udd, 15*time.Second)
	if err != nil {
		logger("%v", err)
		d.shutdown(1)
	}

	wsURL, err := attachPage(port, 10*time.Second)
	if err != nil {
		logger("attach page: %v", err)
		d.shutdown(1)
	}

	client, err := browser.Dial(wsURL)
	if err != nil {
		logger("cdp dial: %v", err)
		d.shutdown(1)
	}
	d.client = client

	ln, err := net.Listen("unix", *socket)
	if err != nil {
		logger("listen %s: %v", *socket, err)
		d.shutdown(1)
	}
	d.ln = ln

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger("received %s, shutting down", sig)
		d.shutdown(0)
	}()

	logger("serving on %s (chrome pid %d, devtools port %d)", *socket, cmd.Process.Pid, port)
	for {
		conn, err := ln.Accept()
		if err != nil {
			// Accept fails once the listener is closed during shutdown.
			if d.closing() {
				return
			}
			logger("accept: %v", err)
			continue
		}
		d.handleConn(conn)
	}
}

// driver holds the long-lived browser session state shared across connections.
type driver struct {
	client *browser.Client
	chrome *exec.Cmd
	udd    string
	socket string
	ln     net.Listener
	logger func(string, ...any)

	// execMu serializes actions; the CDP client handles one request at a time.
	execMu sync.Mutex

	closeOnce sync.Once
	closed    atomicBool
}

func (d *driver) closing() bool { return d.closed.get() }

// shutdown tears everything down once and exits the process with code.
func (d *driver) shutdown(code int) {
	d.closeOnce.Do(func() {
		d.closed.set(true)
		if d.ln != nil {
			_ = d.ln.Close()
		}
		if d.client != nil {
			_ = d.client.Close()
		}
		if d.chrome != nil && d.chrome.Process != nil {
			_ = d.chrome.Process.Kill()
			_, _ = d.chrome.Process.Wait()
		}
		if d.socket != "" {
			_ = os.Remove(d.socket)
		}
		if d.udd != "" {
			_ = os.RemoveAll(d.udd)
		}
	})
	os.Exit(code)
}

// handleConn processes exactly one Command/Result exchange on conn.
func (d *driver) handleConn(conn net.Conn) {
	defer conn.Close()

	// Only the command read gets a deadline; the action itself may run long.
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	var cmd browser.Command
	if err := json.NewDecoder(conn).Decode(&cmd); err != nil {
		writeResult(conn, browser.Result{Error: "decode command: " + err.Error()})
		return
	}
	_ = conn.SetReadDeadline(time.Time{})

	if cmd.Action == "close" {
		writeResult(conn, browser.Result{OK: true})
		_ = conn.Close()
		d.logger("close requested, shutting down")
		d.shutdown(0)
		return
	}
	writeResult(conn, d.execute(cmd))
}

func (d *driver) execute(cmd browser.Command) browser.Result {
	d.execMu.Lock()
	defer d.execMu.Unlock()

	timeout := time.Duration(cmd.TimeoutMS) * time.Millisecond

	switch cmd.Action {
	case "ping":
		return browser.Result{OK: true}
	case "navigate":
		if cmd.URL == "" {
			return errResult("navigate: url is required")
		}
		if err := d.client.Navigate(cmd.URL, timeout); err != nil {
			return errResult(err.Error())
		}
		return browser.Result{OK: true}
	case "eval":
		return valueResult(d.client.Eval(cmd.Expr))
	case "text":
		return valueResult(d.client.Text())
	case "html":
		return valueResult(d.client.HTML())
	case "title":
		return valueResult(d.client.Title())
	case "url":
		return valueResult(d.client.URL())
	case "screenshot":
		b64, err := d.client.Screenshot()
		if err != nil {
			return errResult(err.Error())
		}
		return browser.Result{OK: true, Screenshot: b64}
	case "click":
		if cmd.Selector == "" {
			return errResult("click: selector is required")
		}
		if err := d.client.Click(cmd.Selector); err != nil {
			return errResult(err.Error())
		}
		return browser.Result{OK: true}
	case "type":
		if cmd.Selector == "" {
			return errResult("type: selector is required")
		}
		if err := d.client.Type(cmd.Selector, cmd.Text); err != nil {
			return errResult(err.Error())
		}
		return browser.Result{OK: true}
	case "wait":
		if cmd.Selector == "" {
			return errResult("wait: selector is required")
		}
		if err := d.client.WaitSelector(cmd.Selector, timeout); err != nil {
			return errResult(err.Error())
		}
		return browser.Result{OK: true}
	default:
		return errResult("unknown action: " + cmd.Action)
	}
}

func valueResult(v string, err error) browser.Result {
	if err != nil {
		return errResult(err.Error())
	}
	return browser.Result{OK: true, Value: v}
}

func errResult(msg string) browser.Result {
	return browser.Result{OK: false, Error: msg}
}

func writeResult(conn net.Conn, res browser.Result) {
	_ = json.NewEncoder(conn).Encode(res)
}

// runCall sends one Command to the driver socket and prints the Result.
func runCall(args []string) {
	fs := flag.NewFlagSet("call", flag.ExitOnError)
	socket := fs.String("socket", "", "path to the driver's Unix domain control socket")
	jsonArg := fs.String("json", "", "Command JSON; if empty, read from stdin")
	_ = fs.Parse(args)

	if *socket == "" {
		fmt.Fprintln(os.Stderr, "call: --socket is required")
		os.Exit(2)
	}

	var raw []byte
	if *jsonArg != "" {
		raw = []byte(*jsonArg)
	} else {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "call: read stdin: %v\n", err)
			os.Exit(2)
		}
		raw = b
	}

	var cmd browser.Command
	if err := json.Unmarshal(raw, &cmd); err != nil {
		fmt.Fprintf(os.Stderr, "call: invalid command JSON: %v\n", err)
		os.Exit(2)
	}

	conn, err := net.Dial("unix", *socket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "call: dial %s: %v\n", *socket, err)
		os.Exit(2)
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(cmd); err != nil {
		fmt.Fprintf(os.Stderr, "call: send command: %v\n", err)
		os.Exit(2)
	}
	// Half-close to signal end-of-request.
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}

	var res browser.Result
	if err := json.NewDecoder(conn).Decode(&res); err != nil {
		fmt.Fprintf(os.Stderr, "call: read result: %v\n", err)
		os.Exit(2)
	}

	out, err := json.Marshal(res)
	if err != nil {
		fmt.Fprintf(os.Stderr, "call: encode result: %v\n", err)
		os.Exit(2)
	}
	fmt.Println(string(out))
	if !res.OK {
		fmt.Fprintln(os.Stderr, res.Error)
		os.Exit(1)
	}
}

// waitForDevToolsPort polls for the DevToolsActivePort file Chromium writes
// after launch; line 1 holds the port.
func waitForDevToolsPort(udd string, timeout time.Duration) (int, error) {
	path := filepath.Join(udd, "DevToolsActivePort")
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			line := strings.TrimSpace(string(data))
			if i := strings.IndexByte(line, '\n'); i >= 0 {
				line = line[:i]
			}
			if p, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && p > 0 {
				return p, nil
			}
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("DevToolsActivePort not ready within %s", timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// target is the slice of a DevTools target descriptor we care about.
type target struct {
	Type                 string `json:"type"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// attachPage returns a page-level DevTools WebSocket URL. It retries because
// Chromium's HTTP endpoint becomes ready a beat after the port file appears.
func attachPage(port int, timeout time.Duration) (string, error) {
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		ws, err := newPageWS(base)
		if err == nil && ws != "" {
			return ws, nil
		}
		if err != nil {
			lastErr = err
		}
		if time.Now().After(deadline) {
			if lastErr == nil {
				lastErr = fmt.Errorf("no page target available")
			}
			return "", lastErr
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func newPageWS(base string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	// Newer Chromium requires PUT for /json/new; older accepts GET. Try both.
	for _, method := range []string{http.MethodPut, http.MethodGet} {
		req, err := http.NewRequest(method, base+"/json/new?about:blank", nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var t target
			if json.Unmarshal(body, &t) == nil && t.WebSocketDebuggerURL != "" {
				return t.WebSocketDebuggerURL, nil
			}
		}
	}
	// Fallback: reuse an existing page target.
	resp, err := client.Get(base + "/json")
	if err != nil {
		return "", err
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var targets []target
	if err := json.Unmarshal(body, &targets); err != nil {
		return "", err
	}
	for _, t := range targets {
		if t.Type == "page" && t.WebSocketDebuggerURL != "" {
			return t.WebSocketDebuggerURL, nil
		}
	}
	return "", fmt.Errorf("no page target found")
}

func logf(prefix string) func(string, ...any) {
	return func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, prefix+format+"\n", a...)
	}
}

// atomicBool is a mutex-guarded bool for the shutdown flag.
type atomicBool struct {
	mu sync.Mutex
	v  bool
}

func (b *atomicBool) set(v bool) {
	b.mu.Lock()
	b.v = v
	b.mu.Unlock()
}

func (b *atomicBool) get() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.v
}
