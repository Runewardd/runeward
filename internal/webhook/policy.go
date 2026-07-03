package webhook

// Policy mirrors the enforceable fields of a runeward.dev/v1alpha1
// ClusterPolicy spec. It is a plain value type so the admission decision logic
// can be exercised without a Kubernetes cluster.
type Policy struct {
	// AllowedProfiles is a list of glob patterns; if non-empty, a resource's
	// spec.profile must match at least one.
	AllowedProfiles []string
	// DeniedProfiles is a list of glob patterns; if any matches spec.profile
	// the request is rejected.
	DeniedProfiles []string
	// AllowedNamespaces is a list of glob patterns; if non-empty, the
	// resource's namespace must match at least one.
	AllowedNamespaces []string
	// RequiredLabels lists label keys that must be present on the resource
	// metadata.
	RequiredLabels []string
	// DefaultProfile is applied (mutating) when spec.profile is empty.
	DefaultProfile string
}

// Decide evaluates a Sandbox/Fleet admission request against every policy and
// returns whether it is allowed, the profile it should carry after defaulting,
// and a human-readable reason when denied.
//
// Evaluation proceeds in two phases:
//
//  1. Defaulting: if profileName is empty, the first policy that declares a
//     DefaultProfile supplies it. The returned mutatedProfile always reflects
//     the effective profile (equal to profileName when no default applied).
//  2. Validation: the effective profile, namespace, and labels are checked
//     against every policy. The first violation short-circuits with a reason.
//     An empty pattern list means "no constraint".
//
// When allowed is true the caller may still apply mutatedProfile if it differs
// from the incoming profileName (defaulting occurred).
func Decide(policies []Policy, namespace string, labels map[string]string, profileName string) (allowed bool, mutatedProfile string, reason string) {
	effective := profileName
	if effective == "" {
		for _, p := range policies {
			if p.DefaultProfile != "" {
				effective = p.DefaultProfile
				break
			}
		}
	}

	for _, p := range policies {
		// Denied profiles take precedence: any match rejects.
		for _, pat := range p.DeniedProfiles {
			if matchGlob(pat, effective) {
				return false, effective, "profile " + quote(effective) + " is denied by cluster policy (matched " + quote(pat) + ")"
			}
		}
		// Allowed profiles: when the list is non-empty the profile must match.
		if len(p.AllowedProfiles) > 0 && !matchAny(p.AllowedProfiles, effective) {
			return false, effective, "profile " + quote(effective) + " is not in the cluster policy allowed list"
		}
		// Allowed namespaces: when the list is non-empty the namespace must
		// match. Skipped for cluster-scoped resources (empty namespace), which
		// are not constrained by a namespace allowlist.
		if namespace != "" && len(p.AllowedNamespaces) > 0 && !matchAny(p.AllowedNamespaces, namespace) {
			return false, effective, "namespace " + quote(namespace) + " is not permitted by cluster policy"
		}
		// Required labels must all be present.
		for _, key := range p.RequiredLabels {
			if _, ok := labels[key]; !ok {
				return false, effective, "required label " + quote(key) + " is missing (cluster policy)"
			}
		}
	}

	return true, effective, ""
}

// matchAny reports whether s matches at least one glob pattern.
func matchAny(patterns []string, s string) bool {
	for _, pat := range patterns {
		if matchGlob(pat, s) {
			return true
		}
	}
	return false
}

// quote wraps s in double quotes for readable admission messages without
// pulling in fmt for a hot path.
func quote(s string) string { return "\"" + s + "\"" }

// matchGlob reports whether s matches pattern as an anchored, full-string glob.
// It is a local copy of the matcher in internal/policy so the webhook package
// stays self-contained.
//
// Supported metacharacters:
//
//   - '*' matches any run of characters, INCLUDING '/'.
//   - '?' matches exactly one character (also including '/').
//   - all other characters match themselves literally.
//
// The whole of s must be consumed for a match.
func matchGlob(pattern, s string) bool {
	var (
		si, pi   int
		star     = -1
		starMark int
	)
	for si < len(s) {
		switch {
		case pi < len(pattern) && (pattern[pi] == '?' || pattern[pi] == s[si]):
			si++
			pi++
		case pi < len(pattern) && pattern[pi] == '*':
			star = pi
			starMark = si
			pi++
		case star != -1:
			pi = star + 1
			starMark++
			si = starMark
		default:
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}
