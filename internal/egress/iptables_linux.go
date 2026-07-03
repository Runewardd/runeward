//go:build linux

package egress

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// SetupRedirect installs iptables rules that transparently redirect the pod's
// outbound TCP traffic to a local transparent proxy listening on redirectPort,
// while exempting the proxy's own traffic (matched by proxyUID), loopback, and
// DNS. It is intended to run once, as a privileged init container sharing the
// pod's network namespace, before the sandbox container starts.
//
// The result: any process other than the proxy that opens a TCP connection has
// it redirected to the proxy, which then enforces the egress [Policy]. Because
// enforcement happens in the kernel, an application cannot bypass it by
// ignoring HTTP(S)_PROXY.
func SetupRedirect(proxyUID, redirectPort int) error {
	uid := strconv.Itoa(proxyUID)
	port := strconv.Itoa(redirectPort)
	const chain = "RUNEWARD_OUT"

	// Rules are applied in order. The custom chain is created fresh so the
	// call is idempotent across container restarts.
	steps := [][]string{
		{"-t", "nat", "-N", chain},
		// The proxy's own egress must not be redirected back to itself.
		{"-t", "nat", "-A", chain, "-m", "owner", "--uid-owner", uid, "-j", "RETURN"},
		// Never redirect loopback traffic.
		{"-t", "nat", "-A", chain, "-o", "lo", "-j", "RETURN"},
		// Allow DNS to flow directly so name resolution keeps working.
		{"-t", "nat", "-A", chain, "-p", "udp", "--dport", "53", "-j", "RETURN"},
		{"-t", "nat", "-A", chain, "-p", "tcp", "--dport", "53", "-j", "RETURN"},
		// Redirect everything else (TCP) to the transparent proxy.
		{"-t", "nat", "-A", chain, "-p", "tcp", "-j", "REDIRECT", "--to-ports", port},
		// Hook the chain into OUTPUT for locally generated TCP traffic.
		{"-t", "nat", "-A", "OUTPUT", "-p", "tcp", "-j", chain},
	}

	// Best-effort flush of any prior chain so re-runs don't stack rules.
	_ = run("iptables", "-t", "nat", "-D", "OUTPUT", "-p", "tcp", "-j", chain)
	_ = run("iptables", "-t", "nat", "-F", chain)
	_ = run("iptables", "-t", "nat", "-X", chain)

	for _, args := range steps {
		if err := run("iptables", args...); err != nil {
			return fmt.Errorf("iptables %s: %w", strings.Join(args, " "), err)
		}
	}
	return nil
}

// run executes a command and wraps any failure with its combined output.
func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
