//go:build !linux

package egress

import "errors"

// SetupRedirect is unsupported on non-Linux platforms.
func SetupRedirect(proxyUID, redirectPort int) error {
	return errors.New("iptables egress redirect is only supported on linux")
}
