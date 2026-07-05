package secrets

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// awsTimeout bounds a single Secrets Manager call so a hung endpoint can't
// block sandbox creation indefinitely.
const awsTimeout = 10 * time.Second

// AWSResolver reads secrets from AWS Secrets Manager over its JSON API,
// authenticating requests with AWS Signature Version 4 implemented here from
// the standard library (crypto/hmac + crypto/sha256), so no SDK is required.
//
// A reference has the form:
//
//	aws://<secretId>            return the raw SecretString
//	aws://<secretId>#<jsonKey>  parse SecretString as JSON and return <jsonKey>
//
// where <secretId> is a secret name or ARN. ARNs contain ':' and '/', so the
// whole locator up to an optional '#' is treated verbatim as the SecretId.
type AWSResolver struct {
	// Region selects the regional Secrets Manager endpoint, e.g. us-east-1.
	Region string
	// AccessKeyID and SecretAccessKey are the SigV4 credentials.
	AccessKeyID     string
	SecretAccessKey string
	// SessionToken, when set, is sent as X-Amz-Security-Token for temporary
	// (STS) credentials.
	SessionToken string
	// Endpoint overrides the derived https://secretsmanager.<region>.amazonaws.com/
	// base URL; used by tests to point at an httptest.Server.
	Endpoint string
	// Client is the HTTP client used for the call; nil selects a client with a
	// short default timeout.
	Client *http.Client
}

// AWSResolverFromEnv constructs an AWSResolver from the standard AWS
// environment variables. Missing values surface as errors at Resolve time
// (fail-closed), not at construction, so Default() never fails just because
// AWS is unused.
func AWSResolverFromEnv() AWSResolver {
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	return AWSResolver{
		Region:          region,
		AccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
		SessionToken:    os.Getenv("AWS_SESSION_TOKEN"),
		Client:          &http.Client{Timeout: awsTimeout},
	}
}

// awsGetSecretValueResponse models the subset of the GetSecretValue response we
// consume.
type awsGetSecretValueResponse struct {
	SecretString string `json:"SecretString"`
}

// Resolve fetches a secret from Secrets Manager named by ref.
func (r AWSResolver) Resolve(ctx context.Context, ref string) (string, error) {
	secretID, jsonKey, err := parseAWSRef(ref)
	if err != nil {
		return "", err
	}
	if r.Region == "" {
		return "", fmt.Errorf("secret reference %q: AWS_REGION (or AWS_DEFAULT_REGION) is not set", ref)
	}
	if r.AccessKeyID == "" || r.SecretAccessKey == "" {
		return "", fmt.Errorf("secret reference %q: AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY are not set", ref)
	}

	endpoint := r.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://secretsmanager.%s.amazonaws.com/", r.Region)
	}

	body := []byte(fmt.Sprintf(`{"SecretId":%s}`, strconv.Quote(secretID)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("secret reference %q: build request: %w", ref, err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "secretsmanager.GetSecretValue")
	if err := r.signV4(req, body, time.Now().UTC()); err != nil {
		return "", fmt.Errorf("secret reference %q: sign request: %w", ref, err)
	}

	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: awsTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("secret reference %q: secretsmanager request failed: %w", ref, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("secret reference %q: secretsmanager returned status %d: %s", ref, resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var out awsGetSecretValueResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("secret reference %q: decode secretsmanager response: %w", ref, err)
	}
	if out.SecretString == "" {
		return "", fmt.Errorf("secret reference %q: secret %q has no SecretString", ref, secretID)
	}

	if jsonKey == "" {
		return out.SecretString, nil
	}

	var fields map[string]any
	if err := json.Unmarshal([]byte(out.SecretString), &fields); err != nil {
		return "", fmt.Errorf("secret reference %q: SecretString is not a JSON object (needed for #%s): %w", ref, jsonKey, err)
	}
	raw, ok := fields[jsonKey]
	if !ok {
		return "", fmt.Errorf("secret reference %q: key %q not present in secret %q", ref, jsonKey, secretID)
	}
	val, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("secret reference %q: key %q is not a string", ref, jsonKey)
	}
	if val == "" {
		return "", fmt.Errorf("secret reference %q: key %q is empty", ref, jsonKey)
	}
	return val, nil
}

// parseAWSRef splits aws://<secretId>[#<jsonKey>] into its parts. The secretId
// may be a name or an ARN (which itself contains ':' and '/'), so everything
// before an optional trailing '#' is taken verbatim.
func parseAWSRef(ref string) (secretID, jsonKey string, err error) {
	scheme, rest, ok := splitScheme(ref)
	if !ok || scheme != "aws" {
		return "", "", fmt.Errorf("secret reference %q is not an aws:// reference", ref)
	}
	if hash := strings.LastIndex(rest, "#"); hash >= 0 {
		jsonKey = rest[hash+1:]
		rest = rest[:hash]
		if jsonKey == "" {
			return "", "", fmt.Errorf("secret reference %q: empty key after # (expected aws://<secretId>#<jsonKey>)", ref)
		}
	}
	if rest == "" {
		return "", "", fmt.Errorf("secret reference %q: missing <secretId> (expected aws://<secretId>[#<jsonKey>])", ref)
	}
	return rest, jsonKey, nil
}

// signV4 signs req in place using AWS Signature Version 4 for the
// secretsmanager service, adding X-Amz-Date, X-Amz-Security-Token (when a
// session token is present), and the Authorization header. body is the exact
// request payload (used for the payload hash).
func (r AWSResolver) signV4(req *http.Request, body []byte, now time.Time) error {
	const service = "secretsmanager"
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	req.Header.Set("X-Amz-Date", amzDate)
	if r.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", r.SessionToken)
	}

	host := req.URL.Host
	if host == "" {
		return fmt.Errorf("request URL has no host")
	}
	req.Host = host

	payloadHash := hexSHA256(body)

	// Build the canonical (sorted, ';'-joined) list of signed headers. Host and
	// the x-amz-* headers we set participate in the signature.
	signed := []string{"content-type", "host", "x-amz-date", "x-amz-target"}
	if r.SessionToken != "" {
		signed = append(signed, "x-amz-security-token")
	}
	sort.Strings(signed)
	signedHeaders := strings.Join(signed, ";")

	headerValue := func(name string) string {
		switch name {
		case "host":
			return host
		default:
			return strings.TrimSpace(req.Header.Get(name))
		}
	}
	var canonicalHeaders strings.Builder
	for _, h := range signed {
		canonicalHeaders.WriteString(h)
		canonicalHeaders.WriteString(":")
		canonicalHeaders.WriteString(headerValue(h))
		canonicalHeaders.WriteString("\n")
	}

	canonicalURI := req.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalQuery := req.URL.RawQuery

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	credentialScope := strings.Join([]string{dateStamp, r.Region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		hexSHA256([]byte(canonicalRequest)),
	}, "\n")

	signingKey := hmacSHA256(
		hmacSHA256(
			hmacSHA256(
				hmacSHA256([]byte("AWS4"+r.SecretAccessKey), []byte(dateStamp)),
				[]byte(r.Region),
			),
			[]byte(service),
		),
		[]byte("aws4_request"),
	)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	authorization := fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		r.AccessKeyID, credentialScope, signedHeaders, signature,
	)
	req.Header.Set("Authorization", authorization)
	return nil
}

// hmacSHA256 returns HMAC-SHA256(key, data).
func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// hexSHA256 returns the lowercase hex SHA-256 of data.
func hexSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
