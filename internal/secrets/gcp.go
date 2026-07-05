package secrets

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// gcpTimeout bounds a single Secret Manager call so a hung endpoint can't block
// sandbox creation indefinitely.
const gcpTimeout = 10 * time.Second

// gcpMetadataDefaultBase is the GCE metadata server base used to fetch a
// service-account access token when no token is supplied out of band.
const gcpMetadataDefaultBase = "http://metadata.google.internal"

// GCPResolver reads secrets from Google Secret Manager over its REST API. A
// reference has either the fully-qualified form:
//
//	gcp://projects/<proj>/secrets/<name>/versions/<ver>
//
// or the shorthand, which fills in the project from GOOGLE_CLOUD_PROJECT and
// defaults the version to "latest":
//
//	gcp://<name>            projects/<proj>/secrets/<name>/versions/latest
//	gcp://<name>#<version>  projects/<proj>/secrets/<name>/versions/<version>
//
// Authorization uses an OAuth2 access token (r.Token, else
// GOOGLE_OAUTH_ACCESS_TOKEN, else the GCE metadata server). Service-account
// JWT signing is intentionally out of scope.
type GCPResolver struct {
	// Project is the default GCP project for shorthand references; taken from
	// GOOGLE_CLOUD_PROJECT when constructed via GCPResolverFromEnv.
	Project string
	// Endpoint overrides the https://secretmanager.googleapis.com base URL;
	// used by tests to point at an httptest.Server.
	Endpoint string
	// Token is a bearer access token; when empty, GOOGLE_OAUTH_ACCESS_TOKEN and
	// then the metadata server are tried.
	Token string
	// MetadataBase overrides the GCE metadata server base URL; used by tests.
	MetadataBase string
	// Client is the HTTP client used for calls; nil selects a client with a
	// short default timeout.
	Client *http.Client
}

// GCPResolverFromEnv constructs a GCPResolver from GOOGLE_CLOUD_PROJECT and
// GOOGLE_OAUTH_ACCESS_TOKEN. Missing values surface as errors at Resolve time
// (fail-closed), not at construction, so Default() never fails just because
// GCP is unused.
func GCPResolverFromEnv() GCPResolver {
	return GCPResolver{
		Project: os.Getenv("GOOGLE_CLOUD_PROJECT"),
		Token:   os.Getenv("GOOGLE_OAUTH_ACCESS_TOKEN"),
		Client:  &http.Client{Timeout: gcpTimeout},
	}
}

// gcpAccessResponse models the subset of the AccessSecretVersion response we
// consume: payload.data is standard base64.
type gcpAccessResponse struct {
	Payload struct {
		Data string `json:"data"`
	} `json:"payload"`
}

// gcpMetadataToken models the metadata server's token response.
type gcpMetadataToken struct {
	AccessToken string `json:"access_token"`
}

// Resolve fetches and decodes a secret payload from Secret Manager named by ref.
func (r GCPResolver) Resolve(ctx context.Context, ref string) (string, error) {
	name, err := r.parseGCPRef(ref)
	if err != nil {
		return "", err
	}

	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: gcpTimeout}
	}

	token, err := r.token(ctx, client)
	if err != nil {
		return "", fmt.Errorf("secret reference %q: %w", ref, err)
	}

	base := r.Endpoint
	if base == "" {
		base = "https://secretmanager.googleapis.com"
	}
	base = strings.TrimRight(base, "/")
	endpoint := fmt.Sprintf("%s/v1/%s:access", base, name)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("secret reference %q: build request: %w", ref, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("secret reference %q: secretmanager request failed: %w", ref, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("secret reference %q: secretmanager returned status %d: %s", ref, resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var out gcpAccessResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("secret reference %q: decode secretmanager response: %w", ref, err)
	}
	if out.Payload.Data == "" {
		return "", fmt.Errorf("secret reference %q: secret %s has empty payload", ref, name)
	}
	decoded, err := base64.StdEncoding.DecodeString(out.Payload.Data)
	if err != nil {
		return "", fmt.Errorf("secret reference %q: decode secret payload: %w", ref, err)
	}
	if len(decoded) == 0 {
		return "", fmt.Errorf("secret reference %q: secret %s decoded to empty value", ref, name)
	}
	return string(decoded), nil
}

// token resolves the bearer token: r.Token, then GOOGLE_OAUTH_ACCESS_TOKEN,
// then the GCE metadata server.
func (r GCPResolver) token(ctx context.Context, client *http.Client) (string, error) {
	if r.Token != "" {
		return r.Token, nil
	}
	if t := os.Getenv("GOOGLE_OAUTH_ACCESS_TOKEN"); t != "" {
		return t, nil
	}
	return r.metadataToken(ctx, client)
}

// metadataToken fetches an access token from the GCE metadata server.
func (r GCPResolver) metadataToken(ctx context.Context, client *http.Client) (string, error) {
	base := r.MetadataBase
	if base == "" {
		base = gcpMetadataDefaultBase
	}
	base = strings.TrimRight(base, "/")
	url := base + "/computeMetadata/v1/instance/service-accounts/default/token"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build metadata token request: %w", err)
	}
	req.Header.Set("Metadata-Flavor", "Google")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("no access token: r.Token, GOOGLE_OAUTH_ACCESS_TOKEN unset and metadata server unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("metadata server returned status %d fetching access token", resp.StatusCode)
	}
	var tok gcpMetadataToken
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", fmt.Errorf("decode metadata token response: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("metadata server returned an empty access token")
	}
	return tok.AccessToken, nil
}

// parseGCPRef resolves ref to a fully-qualified resource name of the form
// projects/<proj>/secrets/<name>/versions/<ver>.
func (r GCPResolver) parseGCPRef(ref string) (string, error) {
	scheme, rest, ok := splitScheme(ref)
	if !ok || scheme != "gcp" {
		return "", fmt.Errorf("secret reference %q is not a gcp:// reference", ref)
	}
	if rest == "" {
		return "", fmt.Errorf("secret reference %q: missing <name> (expected gcp://<name>[#<version>] or gcp://projects/<proj>/secrets/<name>/versions/<ver>)", ref)
	}

	// Fully-qualified form is passed through verbatim.
	if strings.HasPrefix(rest, "projects/") {
		if !strings.Contains(rest, "/secrets/") || !strings.Contains(rest, "/versions/") {
			return "", fmt.Errorf("secret reference %q: expected gcp://projects/<proj>/secrets/<name>/versions/<ver>", ref)
		}
		return rest, nil
	}

	// Shorthand: gcp://<name>[#<version>], project from GOOGLE_CLOUD_PROJECT.
	name := rest
	version := "latest"
	if hash := strings.LastIndex(rest, "#"); hash >= 0 {
		name = rest[:hash]
		version = rest[hash+1:]
		if version == "" {
			return "", fmt.Errorf("secret reference %q: empty version after # (expected gcp://<name>#<version>)", ref)
		}
	}
	if name == "" {
		return "", fmt.Errorf("secret reference %q: missing <name> (expected gcp://<name>[#<version>])", ref)
	}
	if strings.Contains(name, "/") {
		return "", fmt.Errorf("secret reference %q: shorthand <name> must not contain '/'; use the full projects/.../secrets/.../versions/... form", ref)
	}
	if r.Project == "" {
		return "", fmt.Errorf("secret reference %q: GOOGLE_CLOUD_PROJECT is not set (needed for shorthand gcp://<name>)", ref)
	}
	return fmt.Sprintf("projects/%s/secrets/%s/versions/%s", r.Project, name, version), nil
}
