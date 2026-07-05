package backend

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net"
	"net/http"

	"github.com/Runewardd/runeward/internal/egress"
	"github.com/Runewardd/runeward/internal/profile"
)

// policyFromNetwork translates a profile's [profile.Network] into an [egress.Policy].
func policyFromNetwork(n profile.Network) egress.Policy {
	def := n.Default
	if def == "" {
		def = "allow"
	}
	rules := make([]egress.Rule, 0, len(n.Rules))
	for _, r := range n.Rules {
		v := r.Verdict
		if v == "" {
			v = "allow"
		}
		rules = append(rules, egress.Rule{Verdict: v, Hostname: r.Hostname, CIDR: r.CIDR})
	}
	return egress.Policy{Default: def, Rules: rules}
}

// proxyEnv returns the HTTP(S)_PROXY variables pointing a sandbox at proxyURL.
func proxyEnv(proxyURL string) map[string]string {
	return map[string]string{
		"HTTP_PROXY":  proxyURL,
		"HTTPS_PROXY": proxyURL,
		"http_proxy":  proxyURL,
		"https_proxy": proxyURL,
		"NO_PROXY":    "localhost,127.0.0.1,::1",
		"no_proxy":    "localhost,127.0.0.1,::1",
	}
}

// hostProxy is an in-process egress proxy on a host port; containers reach it
// via host.docker.internal:<port>.
type hostProxy struct {
	srv  *http.Server
	port int
	// user/pass gate the proxy so that, although it binds a shared interface
	// (host.docker.internal requires a non-loopback bind), only the sandbox we
	// handed the credentials to can use it.
	user string
	pass string
}

// startHostProxy serves the egress proxy for pol on an ephemeral host port,
// protected by freshly-generated Basic credentials.
func startHostProxy(pol egress.Policy, logger *log.Logger) (*hostProxy, error) {
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return nil, err
	}
	user := "runeward"
	pass := randomToken()
	p := &egress.Proxy{Policy: pol, Logger: logger, AuthUser: user, AuthPass: pass}
	srv := &http.Server{Handler: p.Handler()}
	go func() { _ = srv.Serve(ln) }()
	return &hostProxy{srv: srv, port: ln.Addr().(*net.TCPAddr).Port, user: user, pass: pass}, nil
}

// randomToken returns a 128-bit hex secret for proxy authentication.
func randomToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// stop shuts the proxy down. It is safe to call on a nil receiver.
func (h *hostProxy) stop() {
	if h != nil && h.srv != nil {
		_ = h.srv.Close()
	}
}
