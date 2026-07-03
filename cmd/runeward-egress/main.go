// Command runeward-egress is a deny-by-default forward proxy enforcing an
// [egress.Policy] on sandbox traffic (via HTTP_PROXY/HTTPS_PROXY).
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/adefemi171/runeward/internal/egress"
)

func main() {
	addr := flag.String("addr", ":8888", "forward-proxy listen address (cooperative HTTP(S)_PROXY mode)")
	policyPath := flag.String("policy", "", "path to a JSON policy file; if empty, deny all by default")
	transparent := flag.Bool("transparent", false, "run as a transparent proxy for iptables-redirected traffic (linux only)")
	redirectPort := flag.Int("redirect-port", egress.StrictRedirectPort, "transparent proxy listen port / iptables REDIRECT target")
	setupIptables := flag.Bool("setup-iptables", false, "install iptables redirect rules and exit (init-container mode, linux only)")
	proxyUID := flag.Int("proxy-uid", egress.StrictProxyUID, "uid the transparent proxy runs as (exempt from redirect)")
	flag.Parse()

	logger := log.New(os.Stderr, "runeward-egress ", log.LstdFlags|log.LUTC)

	// Init-container mode: install the iptables rules and exit.
	if *setupIptables {
		if err := egress.SetupRedirect(*proxyUID, *redirectPort); err != nil {
			logger.Fatalf("setup-iptables: %v", err)
		}
		logger.Printf("iptables redirect installed (uid=%d -> :%d)", *proxyUID, *redirectPort)
		return
	}

	// Default to deny-all when no policy file is supplied.
	policy := egress.Policy{Default: "deny"}
	if *policyPath != "" {
		p, err := egress.LoadPolicy(*policyPath)
		if err != nil {
			logger.Fatalf("load policy %q: %v", *policyPath, err)
		}
		policy = p
	}

	// Transparent mode: enforce policy on iptables-redirected connections.
	if *transparent {
		tp := &egress.TransparentProxy{Policy: policy, Logger: logger}
		listen := ":" + strconv.Itoa(*redirectPort)
		logger.Printf("transparent proxy listening on %s (default=%q, rules=%d)", listen, policyDefault(policy), len(policy.Rules))
		if err := tp.Serve(listen); err != nil {
			logger.Fatalf("transparent serve: %v", err)
		}
		return
	}

	proxy := &egress.Proxy{Policy: policy, Logger: logger}

	srv := &http.Server{
		Addr:    *addr,
		Handler: proxy.Handler(),
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Printf("listening on %s (default=%q, rules=%d)", *addr, policyDefault(policy), len(policy.Rules))
		errCh <- srv.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			logger.Fatalf("serve: %v", err)
		}
	case sig := <-sigCh:
		logger.Printf("received %s, shutting down", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logger.Fatalf("shutdown: %v", err)
		}
	}
}

func policyDefault(p egress.Policy) string {
	if p.Default == "" {
		return "allow"
	}
	return p.Default
}
