package egress

// Constants shared by the strict (L3) egress enforcement path between the
// Kubernetes backend (which builds the pod spec) and the runeward-egress
// binary (which sets up iptables and runs the transparent proxy).
const (
	// StrictProxyUID is the uid the transparent proxy container runs as. The
	// iptables rules exempt this uid so the proxy's own upstream connections
	// are not redirected back to itself. The sandbox container must run as a
	// different uid for its traffic to be intercepted.
	StrictProxyUID = 1337

	// StrictRedirectPort is the local port the transparent proxy listens on
	// and the iptables REDIRECT target.
	StrictRedirectPort = 15001
)
