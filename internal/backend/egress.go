package backend

import (
	"log"
	"net"
	"net/http"

	"github.com/adefemi171/runeward/internal/egress"
	"github.com/adefemi171/runeward/internal/profile"
)

// policyFromNetwork translates a profile's declarative [profile.Network] into an
// [egress.Policy] the proxy enforces.
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

// proxyEnv returns the HTTP(S)_PROXY environment injected into a sandbox so its
// outbound traffic is routed through the egress proxy reachable at proxyURL.
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

// hostProxy is an in-process egress proxy bound to a host port. The Docker
// backend uses it to constrain a container's egress; the container reaches it
// via host.docker.internal:<port>.
type hostProxy struct {
	srv  *http.Server
	port int
}

// startHostProxy binds an ephemeral host port and serves the egress proxy for
// pol in a background goroutine.
func startHostProxy(pol egress.Policy, logger *log.Logger) (*hostProxy, error) {
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return nil, err
	}
	p := &egress.Proxy{Policy: pol, Logger: logger}
	srv := &http.Server{Handler: p.Handler()}
	go func() { _ = srv.Serve(ln) }()
	return &hostProxy{srv: srv, port: ln.Addr().(*net.TCPAddr).Port}, nil
}

// stop shuts the proxy down. It is safe to call on a nil receiver.
func (h *hostProxy) stop() {
	if h != nil && h.srv != nil {
		_ = h.srv.Close()
	}
}
