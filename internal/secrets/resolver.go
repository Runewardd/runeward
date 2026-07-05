// Package secrets resolves secret references from multiple backends so a
// profile's [[env]] entries can pull values from more than literals and files.
//
// A reference is a URI whose scheme selects the backend:
//
//	env://NAME                       read from the operator's environment
//	vault://<mount>/<path>#<field>   read a HashiCorp Vault KV v2 field
//	aws://<secretId>[#<jsonKey>]     read an AWS Secrets Manager secret
//	gcp://<name>[#<version>]         read a GCP Secret Manager secret version
//	op://vault/item/field            1Password (not built in; errors)
//
// The dispatcher returned by Default routes a reference by scheme. It is
// fail-closed: an unknown scheme, a missing value, or a backend error is
// returned as an error rather than resolving to an empty string, so a sandbox
// never starts believing it holds a credential it does not.
package secrets

import (
	"context"
	"fmt"
	"strings"
)

// Resolver turns a scheme-qualified secret reference into its literal value.
type Resolver interface {
	// Resolve returns the secret value named by ref, or an error if the
	// reference is malformed, the backend is unreachable, or the value is
	// unset. Implementations must not return an empty string with a nil error.
	Resolve(ctx context.Context, ref string) (string, error)
}

// Dispatcher routes a reference to a backend Resolver by its URI scheme.
type Dispatcher struct {
	Env   Resolver
	Vault Resolver
	AWS   Resolver
	GCP   Resolver
}

// Default returns the standard dispatcher, wiring each backend resolver from
// the process environment (Vault: VAULT_ADDR / VAULT_TOKEN; AWS: AWS_REGION /
// AWS_ACCESS_KEY_ID / …; GCP: GOOGLE_CLOUD_PROJECT / GOOGLE_OAUTH_ACCESS_TOKEN).
// Callers pass a reference to Resolve, e.g.
// secrets.Default().Resolve(ctx, "vault://kv/data/db#password").
func Default() Resolver {
	return &Dispatcher{
		Env:   EnvResolver{},
		Vault: VaultResolverFromEnv(),
		AWS:   AWSResolverFromEnv(),
		GCP:   GCPResolverFromEnv(),
	}
}

// Resolve dispatches ref to the resolver for its scheme. Supported schemes are
// env://, vault://, and op:// (the last always errors, by design).
func (d *Dispatcher) Resolve(ctx context.Context, ref string) (string, error) {
	scheme, _, ok := splitScheme(ref)
	if !ok {
		return "", fmt.Errorf("secret reference %q has no scheme; expected one of env://, vault://, aws://, gcp://, op://", ref)
	}
	switch scheme {
	case "env":
		if d.Env == nil {
			return "", fmt.Errorf("secret reference %q: env resolver not configured", ref)
		}
		return d.Env.Resolve(ctx, ref)
	case "vault":
		if d.Vault == nil {
			return "", fmt.Errorf("secret reference %q: vault resolver not configured", ref)
		}
		return d.Vault.Resolve(ctx, ref)
	case "aws":
		if d.AWS == nil {
			return "", fmt.Errorf("secret reference %q: aws resolver not configured", ref)
		}
		return d.AWS.Resolve(ctx, ref)
	case "gcp":
		if d.GCP == nil {
			return "", fmt.Errorf("secret reference %q: gcp resolver not configured", ref)
		}
		return d.GCP.Resolve(ctx, ref)
	case "op":
		return "", fmt.Errorf("secret reference %q: 1Password (op://) resolution is not built in; provide the value another way", ref)
	default:
		return "", fmt.Errorf("secret reference %q: unsupported scheme %q; expected one of env://, vault://, aws://, gcp://, op://", ref, scheme)
	}
}

// splitScheme separates "scheme://rest" without requiring the rest to be a
// valid URL (env://NAME and vault paths are not strict URLs). It reports
// whether a "scheme://" prefix was present.
func splitScheme(ref string) (scheme, rest string, ok bool) {
	i := strings.Index(ref, "://")
	if i <= 0 {
		return "", "", false
	}
	scheme = strings.ToLower(ref[:i])
	// A scheme must be a plausible URI scheme; reject anything with a space so
	// a stray value isn't misread as a reference.
	if strings.ContainsAny(scheme, " \t/") {
		return "", "", false
	}
	return scheme, ref[i+len("://"):], true
}
