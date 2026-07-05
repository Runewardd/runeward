package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// vaultTimeout bounds a single Vault read so a hung server can't block sandbox
// creation indefinitely.
const vaultTimeout = 10 * time.Second

// VaultResolver reads secrets from a HashiCorp Vault KV v2 mount over its HTTP
// API. A reference has the form:
//
//	vault://<mount>/<path>#<field>
//
// e.g. vault://kv/database/prod#password reads field "password" from the
// logical path "database/prod" on the "kv" mount, issuing:
//
//	GET ${Addr}/v1/<mount>/data/<path>   (X-Vault-Token: <Token>)
//
// and returning .data.data.<field> from the JSON response.
type VaultResolver struct {
	// Addr is the Vault base URL, e.g. https://vault:8200 (no trailing /v1).
	Addr string
	// Token authenticates the read; sent as the X-Vault-Token header.
	Token string
	// Client is the HTTP client used for the read; nil selects a client with a
	// short default timeout.
	Client *http.Client
}

// VaultResolverFromEnv constructs a VaultResolver from VAULT_ADDR and
// VAULT_TOKEN. Missing values surface as errors at Resolve time (fail-closed),
// not at construction, so Default() never fails just because Vault is unused.
func VaultResolverFromEnv() VaultResolver {
	return VaultResolver{
		Addr:   strings.TrimRight(os.Getenv("VAULT_ADDR"), "/"),
		Token:  os.Getenv("VAULT_TOKEN"),
		Client: &http.Client{Timeout: vaultTimeout},
	}
}

// vaultKVResponse models the subset of a KV v2 read we consume: the nested
// data.data object holding the field/value pairs.
type vaultKVResponse struct {
	Data struct {
		Data map[string]any `json:"data"`
	} `json:"data"`
}

// Resolve reads a single field from a Vault KV v2 secret named by ref.
func (r VaultResolver) Resolve(ctx context.Context, ref string) (string, error) {
	mount, path, field, err := parseVaultRef(ref)
	if err != nil {
		return "", err
	}
	addr := strings.TrimRight(r.Addr, "/")
	if addr == "" {
		return "", fmt.Errorf("secret reference %q: VAULT_ADDR is not set", ref)
	}
	if r.Token == "" {
		return "", fmt.Errorf("secret reference %q: VAULT_TOKEN is not set", ref)
	}

	endpoint := fmt.Sprintf("%s/v1/%s/data/%s", addr, mount, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("secret reference %q: build request: %w", ref, err)
	}
	req.Header.Set("X-Vault-Token", r.Token)

	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: vaultTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("secret reference %q: vault request failed: %w", ref, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("secret reference %q: vault returned status %d for %s/%s", ref, resp.StatusCode, mount, path)
	}

	var kv vaultKVResponse
	if err := json.NewDecoder(resp.Body).Decode(&kv); err != nil {
		return "", fmt.Errorf("secret reference %q: decode vault response: %w", ref, err)
	}
	raw, ok := kv.Data.Data[field]
	if !ok {
		return "", fmt.Errorf("secret reference %q: field %q not present in secret %s/%s", ref, field, mount, path)
	}

	val, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("secret reference %q: field %q is not a string", ref, field)
	}
	if val == "" {
		return "", fmt.Errorf("secret reference %q: field %q is empty", ref, field)
	}
	return val, nil
}

// parseVaultRef splits vault://<mount>/<path>#<field> into its parts.
func parseVaultRef(ref string) (mount, path, field string, err error) {
	scheme, rest, ok := splitScheme(ref)
	if !ok || scheme != "vault" {
		return "", "", "", fmt.Errorf("secret reference %q is not a vault:// reference", ref)
	}
	hash := strings.LastIndex(rest, "#")
	if hash < 0 {
		return "", "", "", fmt.Errorf("secret reference %q: missing #<field> (expected vault://<mount>/<path>#<field>)", ref)
	}
	field = rest[hash+1:]
	locator := rest[:hash]
	if field == "" {
		return "", "", "", fmt.Errorf("secret reference %q: empty field after # (expected vault://<mount>/<path>#<field>)", ref)
	}

	locator = strings.TrimPrefix(locator, "/")
	slash := strings.Index(locator, "/")
	if slash < 0 {
		return "", "", "", fmt.Errorf("secret reference %q: missing <mount>/<path> (expected vault://<mount>/<path>#<field>)", ref)
	}
	mount = locator[:slash]
	path = strings.Trim(locator[slash+1:], "/")
	if mount == "" || path == "" {
		return "", "", "", fmt.Errorf("secret reference %q: missing <mount>/<path> (expected vault://<mount>/<path>#<field>)", ref)
	}
	return mount, path, field, nil
}
