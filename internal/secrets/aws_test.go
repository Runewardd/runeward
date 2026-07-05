package secrets

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestAWSResolver(endpoint string) AWSResolver {
	return AWSResolver{
		Region:          "us-east-1",
		AccessKeyID:     "AKIAEXAMPLE",
		SecretAccessKey: "secretkey",
		Endpoint:        endpoint,
		Client:          &http.Client{},
	}
}

func TestAWSResolveSecretString(t *testing.T) {
	var gotAuth, gotTarget, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotTarget = r.Header.Get("X-Amz-Target")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		_, _ = io.WriteString(w, `{"SecretString":"s3cret"}`)
	}))
	defer srv.Close()

	r := newTestAWSResolver(srv.URL + "/")
	got, err := r.Resolve(context.Background(), "aws://prod/db-password")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "s3cret" {
		t.Fatalf("value = %q, want %q", got, "s3cret")
	}
	if !strings.HasPrefix(gotAuth, "AWS4-HMAC-SHA256 ") {
		t.Errorf("Authorization = %q, want AWS4-HMAC-SHA256 prefix", gotAuth)
	}
	if !strings.Contains(gotAuth, "Credential=AKIAEXAMPLE/") ||
		!strings.Contains(gotAuth, "SignedHeaders=") ||
		!strings.Contains(gotAuth, "Signature=") {
		t.Errorf("Authorization missing SigV4 components: %q", gotAuth)
	}
	if gotTarget != "secretsmanager.GetSecretValue" {
		t.Errorf("X-Amz-Target = %q", gotTarget)
	}
	if gotBody != `{"SecretId":"prod/db-password"}` {
		t.Errorf("body = %q", gotBody)
	}
}

func TestAWSResolveJSONKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"SecretString":"{\"password\":\"p\",\"user\":\"u\"}"}`)
	}))
	defer srv.Close()

	r := newTestAWSResolver(srv.URL + "/")
	got, err := r.Resolve(context.Background(), "aws://prod/creds#password")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "p" {
		t.Fatalf("value = %q, want %q", got, "p")
	}
}

func TestAWSResolveARN(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{"SecretString":"arn-value"}`)
	}))
	defer srv.Close()

	arn := "arn:aws:secretsmanager:us-east-1:123456789012:secret:prod/db-AbCdEf"
	r := newTestAWSResolver(srv.URL + "/")
	got, err := r.Resolve(context.Background(), "aws://"+arn)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "arn-value" {
		t.Fatalf("value = %q", got)
	}
	if !strings.Contains(gotBody, arn) {
		t.Errorf("body %q does not carry ARN verbatim", gotBody)
	}
}

func TestAWSResolveSessionToken(t *testing.T) {
	var gotSecurityToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSecurityToken = r.Header.Get("X-Amz-Security-Token")
		_, _ = io.WriteString(w, `{"SecretString":"v"}`)
	}))
	defer srv.Close()

	r := newTestAWSResolver(srv.URL + "/")
	r.SessionToken = "session-token-xyz"
	if _, err := r.Resolve(context.Background(), "aws://name"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if gotSecurityToken != "session-token-xyz" {
		t.Errorf("X-Amz-Security-Token = %q", gotSecurityToken)
	}
}

func TestAWSResolveMissingCreds(t *testing.T) {
	r := AWSResolver{Region: "us-east-1", Endpoint: "http://127.0.0.1:0/", Client: &http.Client{}}
	_, err := r.Resolve(context.Background(), "aws://name")
	if err == nil {
		t.Fatal("expected error for missing credentials")
	}
	if !strings.Contains(err.Error(), "AWS_ACCESS_KEY_ID") {
		t.Errorf("error = %v", err)
	}
}

func TestAWSResolveMissingRegion(t *testing.T) {
	r := AWSResolver{AccessKeyID: "a", SecretAccessKey: "b", Client: &http.Client{}}
	_, err := r.Resolve(context.Background(), "aws://name")
	if err == nil || !strings.Contains(err.Error(), "AWS_REGION") {
		t.Fatalf("expected region error, got %v", err)
	}
}

func TestAWSResolveNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"__type":"ResourceNotFoundException"}`)
	}))
	defer srv.Close()

	r := newTestAWSResolver(srv.URL + "/")
	_, err := r.Resolve(context.Background(), "aws://missing")
	if err == nil || !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("expected non-200 error, got %v", err)
	}
}

func TestAWSResolveMissingJSONKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"SecretString":"{\"user\":\"u\"}"}`)
	}))
	defer srv.Close()

	r := newTestAWSResolver(srv.URL + "/")
	_, err := r.Resolve(context.Background(), "aws://creds#password")
	if err == nil || !strings.Contains(err.Error(), "not present") {
		t.Fatalf("expected missing-key error, got %v", err)
	}
}

func TestParseAWSRef(t *testing.T) {
	tests := []struct {
		ref     string
		id      string
		key     string
		wantErr bool
	}{
		{"aws://name", "name", "", false},
		{"aws://name#field", "name", "field", false},
		{"aws://arn:aws:secretsmanager:us-east-1:1:secret:x/y", "arn:aws:secretsmanager:us-east-1:1:secret:x/y", "", false},
		{"aws://", "", "", true},
		{"aws://name#", "", "", true},
		{"vault://x", "", "", true},
	}
	for _, tt := range tests {
		id, key, err := parseAWSRef(tt.ref)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseAWSRef(%q): expected error", tt.ref)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseAWSRef(%q): %v", tt.ref, err)
			continue
		}
		if id != tt.id || key != tt.key {
			t.Errorf("parseAWSRef(%q) = (%q,%q), want (%q,%q)", tt.ref, id, key, tt.id, tt.key)
		}
	}
}
