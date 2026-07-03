// Package egress implements a deny-by-default forward proxy used to
// constrain the network traffic emitted by a sandbox. Sandbox processes
// are pointed at the proxy through the HTTP_PROXY/HTTPS_PROXY environment
// variables; the proxy only permits connections whose destination matches
// an allowlist [Policy].
//
// The package depends solely on the Go standard library.
package egress

import (
	"encoding/json"
	"net"
	"os"
	"strings"
)

// Rule is a single allow/deny decision in a [Policy]. A rule matches a
// connection when either its Hostname matches the destination host (exact
// or wildcard, see [Policy.Allow]) or its CIDR contains the destination IP
// (see [Policy.AllowAddr]). Exactly one of Hostname or CIDR is expected to
// be set for a given rule.
type Rule struct {
	// Verdict is either "allow" or "deny". Any value other than "allow"
	// is treated as a deny.
	Verdict string `json:"verdict"`
	// Hostname matches a destination host by exact match or by a leading
	// "*." wildcard (e.g. "*.example.com").
	Hostname string `json:"hostname"`
	// CIDR matches a destination IP address (e.g. "10.0.0.0/8").
	CIDR string `json:"cidr"`
}

// Policy is an ordered list of [Rule]s evaluated against a destination.
// The first matching rule wins; if no rule matches, Default applies.
type Policy struct {
	// Default is applied when no rule matches. It is either "allow" or
	// "deny". An empty value is treated as "allow".
	Default string `json:"default"`
	// Rules are evaluated in order; the first match decides the verdict.
	Rules []Rule `json:"rules"`
}

// verdictAllows reports whether a verdict string permits the connection.
// Only the exact value "allow" (case-insensitive) permits; everything else
// (including the empty string) denies.
func verdictAllows(verdict string) bool {
	return strings.EqualFold(strings.TrimSpace(verdict), "allow")
}

// defaultAllows reports the fallback decision when no rule matches. An
// empty Default is treated as "allow"; any other non-"allow" value denies.
func (p Policy) defaultAllows() bool {
	if strings.TrimSpace(p.Default) == "" {
		return true
	}
	return verdictAllows(p.Default)
}

// hostnameMatches reports whether pattern matches host. Matching is
// case-insensitive. A pattern beginning with "*." matches any subdomain of
// the remaining suffix at any depth (e.g. "*.example.com" matches
// "api.example.com" and "a.b.example.com") but not the bare domain itself.
// All other patterns match by exact equality.
func hostnameMatches(pattern, host string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	host = strings.ToLower(strings.TrimSpace(host))
	if pattern == "" {
		return false
	}
	if suffix, ok := strings.CutPrefix(pattern, "*."); ok {
		// Require at least one label before the suffix, so the bare
		// domain does not match a subdomain wildcard.
		return strings.HasSuffix(host, "."+suffix) && len(host) > len(suffix)+1
	}
	return pattern == host
}

// Allow reports whether host is permitted by the policy. Only rules
// carrying a Hostname are considered; the first matching rule decides, and
// if none match the policy Default applies. Evaluation is case-insensitive.
func (p Policy) Allow(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, r := range p.Rules {
		if r.Hostname == "" {
			continue
		}
		if hostnameMatches(r.Hostname, host) {
			return verdictAllows(r.Verdict)
		}
	}
	return p.defaultAllows()
}

// AllowAddr reports whether the destination "host:port" is permitted. The
// host portion is evaluated against Hostname rules; if it parses as an IP
// address, CIDR rules are also considered. Rules are evaluated in order and
// the first match (hostname or CIDR) decides. If no rule matches, the
// policy Default applies.
func (p Policy) AllowAddr(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		// No port present; treat the whole value as the host.
		host = hostport
	}
	host = strings.ToLower(strings.TrimSpace(host))
	ip := net.ParseIP(host)

	for _, r := range p.Rules {
		if r.Hostname != "" && hostnameMatches(r.Hostname, host) {
			return verdictAllows(r.Verdict)
		}
		if r.CIDR != "" && ip != nil {
			if _, network, err := net.ParseCIDR(r.CIDR); err == nil && network.Contains(ip) {
				return verdictAllows(r.Verdict)
			}
		}
	}
	return p.defaultAllows()
}

// LoadPolicy reads and decodes a JSON [Policy] from the file at path.
func LoadPolicy(path string) (Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, err
	}
	var p Policy
	if err := json.Unmarshal(data, &p); err != nil {
		return Policy{}, err
	}
	return p, nil
}
