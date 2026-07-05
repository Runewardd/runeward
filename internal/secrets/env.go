package secrets

import (
	"context"
	"fmt"
	"os"
)

// EnvResolver resolves env://NAME references from the operator's process
// environment. It is fail-closed: an unset or empty variable is an error.
type EnvResolver struct{}

// Resolve reads the environment variable named in an env://NAME reference.
func (EnvResolver) Resolve(_ context.Context, ref string) (string, error) {
	scheme, name, ok := splitScheme(ref)
	if !ok || scheme != "env" {
		return "", fmt.Errorf("secret reference %q is not an env:// reference", ref)
	}
	if name == "" {
		return "", fmt.Errorf("secret reference %q: missing variable name (expected env://NAME)", ref)
	}
	v, present := os.LookupEnv(name)
	if !present || v == "" {
		return "", fmt.Errorf("secret reference %q: environment variable %q is unset or empty", ref, name)
	}
	return v, nil
}
