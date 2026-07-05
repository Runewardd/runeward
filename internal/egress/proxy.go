package egress

import (
	"crypto/subtle"
	"encoding/base64"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

// dialTimeout bounds dials to origins and CONNECT targets.
const dialTimeout = 30 * time.Second

// Proxy is a forward proxy that enforces Policy on CONNECT tunnels (HTTPS)
// and plain absolute-URI HTTP requests.
type Proxy struct {
	Policy Policy
	// Logger receives allow/deny decisions; nil discards them.
	Logger *log.Logger
	// AuthUser/AuthPass, when both set, require Proxy-Authorization (HTTP Basic)
	// on every request. This keeps a proxy bound on a shared interface (e.g. the
	// host proxy reachable via host.docker.internal) from being used by other
	// local/LAN processes.
	AuthUser string
	AuthPass string
	// transport forwards plain HTTP requests; nil falls back to the default.
	transport http.RoundTripper
}

func (p *Proxy) logf(format string, args ...any) {
	if p.Logger != nil {
		p.Logger.Printf(format, args...)
	}
}

// authOK reports whether r satisfies the configured proxy credentials. When no
// credentials are configured it always passes.
func (p *Proxy) authOK(r *http.Request) bool {
	if p.AuthUser == "" && p.AuthPass == "" {
		return true
	}
	user, pass, ok := parseProxyBasicAuth(r.Header.Get("Proxy-Authorization"))
	if !ok {
		return false
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(p.AuthUser)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(p.AuthPass)) == 1
	return userOK && passOK
}

// Handler returns an [http.Handler] implementing the forward proxy.
func (p *Proxy) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !p.authOK(r) {
			w.Header().Set("Proxy-Authenticate", `Basic realm="runeward-egress"`)
			http.Error(w, "Proxy Authentication Required", http.StatusProxyAuthRequired)
			return
		}
		if r.Method == http.MethodConnect {
			p.handleConnect(w, r)
			return
		}
		p.handleHTTP(w, r)
	})
}

// handleConnect checks a CONNECT target against the policy, then hijacks the
// client connection and splices it to the target.
func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	target := r.Host // for CONNECT this is the "host:port" authority
	if !p.Policy.AllowAddr(target) {
		p.logf("egress: DENY CONNECT %s", target)
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	upstream, err := net.DialTimeout("tcp", target, dialTimeout)
	if err != nil {
		p.logf("egress: ALLOW CONNECT %s (dial failed: %v)", target, err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}
	defer upstream.Close()

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer client.Close()

	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	p.logf("egress: ALLOW CONNECT %s", target)

	// Splice both directions; return once both are done.
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
	<-done
	<-done
}

// handleHTTP checks a plain forward-proxy request against the policy and, if
// allowed, forwards it to the origin.
func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	// A forward-proxy request carries an absolute URL with a host set.
	host := r.URL.Host
	if host == "" {
		host = r.Host
	}
	if !p.Policy.AllowAddr(host) {
		p.logf("egress: DENY HTTP %s %s", r.Method, host)
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	p.logf("egress: ALLOW HTTP %s %s", r.Method, host)

	transport := p.transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	// RequestURI must be empty on client requests.
	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""

	resp, err := transport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// parseProxyBasicAuth parses a "Basic base64(user:pass)" Proxy-Authorization
// header value.
func parseProxyBasicAuth(header string) (user, pass string, ok bool) {
	const prefix = "Basic "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", "", false
	}
	dec, err := base64.StdEncoding.DecodeString(header[len(prefix):])
	if err != nil {
		return "", "", false
	}
	u, p, found := strings.Cut(string(dec), ":")
	if !found {
		return "", "", false
	}
	return u, p, true
}

func copyHeader(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// ListenAndServe serves the proxy handler on addr until an error occurs.
func (p *Proxy) ListenAndServe(addr string) error {
	srv := &http.Server{
		Addr:    addr,
		Handler: p.Handler(),
	}
	return srv.ListenAndServe()
}
