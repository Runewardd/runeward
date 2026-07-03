package egress

import (
	"io"
	"log"
	"net"
	"net/http"
	"time"
)

// dialTimeout bounds how long the proxy waits when dialing an origin or
// CONNECT target.
const dialTimeout = 30 * time.Second

// Proxy is a deny-by-default forward proxy that enforces Policy on both
// HTTP CONNECT tunnels (used for HTTPS) and plain absolute-URI HTTP
// requests. The zero value is not usable; construct one with a Policy.
type Proxy struct {
	// Policy decides which destinations are reachable.
	Policy Policy
	// Logger receives allow/deny decisions. If nil, logging is discarded.
	Logger *log.Logger
	// transport forwards plain HTTP requests. If nil, a default transport
	// is used lazily via forwardHTTP.
	transport http.RoundTripper
}

// logf logs a decision, discarding output when Logger is nil.
func (p *Proxy) logf(format string, args ...any) {
	if p.Logger != nil {
		p.Logger.Printf(format, args...)
	}
}

// Handler returns an [http.Handler] implementing the forward proxy. CONNECT
// requests are tunneled after an allow check; all other requests are treated
// as plain HTTP forward-proxy requests.
func (p *Proxy) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			p.handleConnect(w, r)
			return
		}
		p.handleHTTP(w, r)
	})
}

// handleConnect services an HTTP CONNECT request. It extracts the target
// host:port from the request line, applies the policy, and on success
// hijacks the client connection, dials the target, and splices bytes
// bidirectionally between client and target until either side closes.
func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	target := r.Host // For CONNECT this is the "host:port" authority.
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

	// Splice the two connections. Each direction runs in its own
	// goroutine; the function returns once both directions are done.
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
	<-done
	<-done
}

// handleHTTP services a plain (non-CONNECT) forward-proxy request. Such
// requests carry an absolute URI whose host is checked against the policy.
// Allowed requests are forwarded to the origin and the response copied back.
func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	// A forward-proxy request has an absolute URL with a host set.
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

	// Build an outbound request from the inbound one. RequestURI must be
	// empty for client requests.
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

// copyHeader copies all header values from src into dst.
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
