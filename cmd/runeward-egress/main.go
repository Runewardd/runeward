// Command runeward-egress is a deny-by-default forward proxy enforcing an
// [egress.Policy] on sandbox traffic (via HTTP_PROXY/HTTPS_PROXY).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Runewardd/runeward/internal/egress"
)

// policyEnv lets the Docker backend pass the egress policy inline (JSON) instead
// of a mounted file, so no host bind-mount is needed for the sidecar.
const policyEnv = "RUNEWARD_EGRESS_POLICY"

// dropPrivileges switches the process to uid/gid so the transparent proxy's own
// upstream traffic runs as the iptables-exempt uid. Since Go 1.16 syscall.Setuid
// applies process-wide. Used by the combined setup+serve (Docker) mode.
func dropPrivileges(uid int) error {
	if err := syscall.Setgid(uid); err != nil {
		return err
	}
	return syscall.Setuid(uid)
}

// loadPolicy resolves the egress policy from --policy (file) or the
// RUNEWARD_EGRESS_POLICY env var (inline JSON); empty means deny-all.
func loadPolicy(path string) (egress.Policy, error) {
	if path != "" {
		return egress.LoadPolicy(path)
	}
	if raw := os.Getenv(policyEnv); raw != "" {
		var p egress.Policy
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return egress.Policy{}, err
		}
		return p, nil
	}
	return egress.Policy{Default: "deny"}, nil
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8888", "forward-proxy listen address (cooperative HTTP(S)_PROXY mode); loopback by default")
	policyPath := flag.String("policy", "", "path to a JSON policy file; if empty, deny all by default")
	transparent := flag.Bool("transparent", false, "run as a transparent proxy for iptables-redirected traffic (linux only)")
	redirectPort := flag.Int("redirect-port", egress.StrictRedirectPort, "transparent proxy listen port / iptables REDIRECT target")
	setupIptables := flag.Bool("setup-iptables", false, "install iptables redirect rules and exit (init-container mode, linux only)")
	proxyUID := flag.Int("proxy-uid", egress.StrictProxyUID, "uid the transparent proxy runs as (exempt from redirect)")
	flag.Parse()

	logger := log.New(os.Stderr, "runeward-egress ", log.LstdFlags|log.LUTC)

	if *setupIptables {
		if err := egress.SetupRedirect(*proxyUID, *redirectPort); err != nil {
			logger.Fatalf("setup-iptables: %v", err)
		}
		logger.Printf("iptables redirect installed (uid=%d -> :%d)", *proxyUID, *redirectPort)
		if !*transparent {
			// Init-container mode: rules installed, nothing more to do.
			return
		}
		// Combined mode (Docker single-container sidecar): drop to the exempt
		// uid, then fall through to serve the transparent proxy below.
		if err := dropPrivileges(*proxyUID); err != nil {
			logger.Fatalf("drop privileges to uid %d: %v", *proxyUID, err)
		}
		logger.Printf("dropped privileges to uid %d", *proxyUID)
	}

	policy, err := loadPolicy(*policyPath)
	if err != nil {
		logger.Fatalf("load policy: %v", err)
	}

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
