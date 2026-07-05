//go:build linux

package egress

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// dnsResolversEnv optionally restricts DNS egress to a comma-separated list of
// resolver IPs. When set, DNS (port 53) to any other destination is dropped, so
// a workload can't tunnel data out over DNS to an arbitrary server. Unset keeps
// DNS open (the previous behavior).
const dnsResolversEnv = "RUNEWARD_DNS_RESOLVERS"

// parseDNSResolvers returns the validated resolver IPs configured via
// RUNEWARD_DNS_RESOLVERS, or nil when unset/empty.
func parseDNSResolvers() []string {
	raw := strings.TrimSpace(os.Getenv(dnsResolversEnv))
	if raw == "" {
		return nil
	}
	var out []string
	for _, f := range strings.Split(raw, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if net.ParseIP(f) == nil {
			continue // skip garbage rather than fail the whole setup
		}
		out = append(out, f)
	}
	return out
}

// SetupRedirect installs iptables rules that redirect outbound TCP to the
// transparent proxy on redirectPort, exempting the proxy's own uid, loopback,
// and DNS. Runs once from a privileged init container before the sandbox
// starts. Enforcement happens in the kernel, so an app can't opt out by
// ignoring HTTP(S)_PROXY.
//
// Beyond TCP redirect it also closes two bypass paths the proxy can't see:
//   - non-DNS UDP is dropped, so QUIC/HTTP3 (443/udp) can't tunnel around the
//     TCP-only proxy — clients fall back to TCP, which is proxied.
//   - IPv6 egress is dropped (except loopback and DNS), since the proxy is
//     IPv4-only; this forces traffic onto the proxied IPv4 path.
func SetupRedirect(proxyUID, redirectPort int) error {
	uid := strconv.Itoa(proxyUID)
	port := strconv.Itoa(redirectPort)
	const chain = "RUNEWARD_OUT"
	const udpChain = "RUNEWARD_UDP"
	const dnsTCPChain = "RUNEWARD_DNST"

	resolvers := parseDNSResolvers()

	steps := [][]string{
		{"-t", "nat", "-N", chain},
		// Don't redirect the proxy's own egress back to itself.
		{"-t", "nat", "-A", chain, "-m", "owner", "--uid-owner", uid, "-j", "RETURN"},
		{"-t", "nat", "-A", chain, "-o", "lo", "-j", "RETURN"},
		// Let DNS through (not redirected) so name resolution keeps working;
		// destination filtering, if any, happens in the filter table below.
		{"-t", "nat", "-A", chain, "-p", "udp", "--dport", "53", "-j", "RETURN"},
		{"-t", "nat", "-A", chain, "-p", "tcp", "--dport", "53", "-j", "RETURN"},
		{"-t", "nat", "-A", chain, "-p", "tcp", "-j", "REDIRECT", "--to-ports", port},
		{"-t", "nat", "-A", "OUTPUT", "-p", "tcp", "-j", chain},

		// Drop non-DNS UDP (QUIC/HTTP3 bypass) via a filter-table chain.
		{"-t", "filter", "-N", udpChain},
		{"-t", "filter", "-A", udpChain, "-m", "owner", "--uid-owner", uid, "-j", "RETURN"},
		{"-t", "filter", "-A", udpChain, "-o", "lo", "-j", "RETURN"},
	}
	// DNS destination policy for UDP: allow only configured resolvers when set,
	// otherwise allow any :53. Non-DNS UDP is always dropped by the trailing rule.
	if len(resolvers) > 0 {
		for _, ip := range resolvers {
			steps = append(steps, []string{"-t", "filter", "-A", udpChain, "-p", "udp", "-d", ip, "--dport", "53", "-j", "RETURN"})
		}
		// Non-resolver :53 falls through to the final DROP.
	} else {
		steps = append(steps, []string{"-t", "filter", "-A", udpChain, "-p", "udp", "--dport", "53", "-j", "RETURN"})
	}
	steps = append(steps,
		[]string{"-t", "filter", "-A", udpChain, "-p", "udp", "-j", "DROP"},
		[]string{"-t", "filter", "-A", "OUTPUT", "-p", "udp", "-j", udpChain},
	)

	// When resolvers are pinned, also confine TCP DNS (DoT/large responses) to
	// those hosts so DNS can't be a covert channel over TCP either.
	if len(resolvers) > 0 {
		steps = append(steps,
			[]string{"-t", "filter", "-N", dnsTCPChain},
			[]string{"-t", "filter", "-A", dnsTCPChain, "-m", "owner", "--uid-owner", uid, "-j", "RETURN"},
			[]string{"-t", "filter", "-A", dnsTCPChain, "-o", "lo", "-j", "RETURN"},
		)
		for _, ip := range resolvers {
			steps = append(steps, []string{"-t", "filter", "-A", dnsTCPChain, "-p", "tcp", "-d", ip, "--dport", "53", "-j", "RETURN"})
		}
		steps = append(steps,
			[]string{"-t", "filter", "-A", dnsTCPChain, "-p", "tcp", "--dport", "53", "-j", "DROP"},
			[]string{"-t", "filter", "-A", "OUTPUT", "-p", "tcp", "--dport", "53", "-j", dnsTCPChain},
		)
	}

	// Best-effort flush of any prior chains so re-runs don't stack rules.
	_ = run("iptables", "-t", "nat", "-D", "OUTPUT", "-p", "tcp", "-j", chain)
	_ = run("iptables", "-t", "nat", "-F", chain)
	_ = run("iptables", "-t", "nat", "-X", chain)
	_ = run("iptables", "-t", "filter", "-D", "OUTPUT", "-p", "udp", "-j", udpChain)
	_ = run("iptables", "-t", "filter", "-F", udpChain)
	_ = run("iptables", "-t", "filter", "-X", udpChain)
	_ = run("iptables", "-t", "filter", "-D", "OUTPUT", "-p", "tcp", "--dport", "53", "-j", dnsTCPChain)
	_ = run("iptables", "-t", "filter", "-F", dnsTCPChain)
	_ = run("iptables", "-t", "filter", "-X", dnsTCPChain)

	for _, args := range steps {
		if err := run("iptables", args...); err != nil {
			return fmt.Errorf("iptables %s: %w", strings.Join(args, " "), err)
		}
	}

	// Close the IPv6 bypass. The proxy is IPv4-only, so drop all IPv6 egress
	// except loopback and DNS. Best-effort: some environments lack IPv6 /
	// ip6tables entirely, which is fine (nothing to bypass through).
	setupDropIPv6()
	return nil
}

// setupDropIPv6 drops outbound IPv6 (except loopback and DNS) so traffic can't
// escape the IPv4-only proxy over v6. Failures are ignored: a host without
// IPv6 or ip6tables has no v6 path to bypass in the first place.
func setupDropIPv6() {
	const chain6 = "RUNEWARD_OUT6"
	_ = run("ip6tables", "-D", "OUTPUT", "-j", chain6)
	_ = run("ip6tables", "-F", chain6)
	_ = run("ip6tables", "-X", chain6)

	steps := [][]string{
		{"-N", chain6},
		{"-A", chain6, "-o", "lo", "-j", "RETURN"},
		{"-A", chain6, "-p", "udp", "--dport", "53", "-j", "RETURN"},
		{"-A", chain6, "-p", "tcp", "--dport", "53", "-j", "RETURN"},
		{"-A", chain6, "-j", "DROP"},
		{"-A", "OUTPUT", "-j", chain6},
	}
	for _, args := range steps {
		_ = run("ip6tables", args...)
	}
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
