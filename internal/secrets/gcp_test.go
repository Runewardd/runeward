package secrets

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGCPResolveShorthand(t *testing.T) {
	var gotAuth, gotPath string
	value := "hunter2"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		enc := base64.StdEncoding.EncodeToString([]byte(value))
		_, _ = io.WriteString(w, `{"payload":{"data":"`+enc+`"}}`)
	}))
	defer srv.Close()

	r := GCPResolver{Project: "my-proj", Endpoint: srv.URL, Token: "tok-123", Client: &http.Client{}}
	got, err := r.Resolve(context.Background(), "gcp://db-password")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != value {
		t.Fatalf("value = %q, want %q", got, value)
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotPath != "/v1/projects/my-proj/secrets/db-password/versions/latest:access" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestGCPResolveVersionAndFullForm(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		enc := base64.StdEncoding.EncodeToString([]byte("v"))
		_, _ = io.WriteString(w, `{"payload":{"data":"`+enc+`"}}`)
	}))
	defer srv.Close()

	r := GCPResolver{Project: "my-proj", Endpoint: srv.URL, Token: "tok", Client: &http.Client{}}

	if _, err := r.Resolve(context.Background(), "gcp://name#3"); err != nil {
		t.Fatalf("Resolve shorthand#ver: %v", err)
	}
	if gotPath != "/v1/projects/my-proj/secrets/name/versions/3:access" {
		t.Errorf("shorthand version path = %q", gotPath)
	}

	full := "gcp://projects/other/secrets/thing/versions/7"
	if _, err := r.Resolve(context.Background(), full); err != nil {
		t.Fatalf("Resolve full form: %v", err)
	}
	if gotPath != "/v1/projects/other/secrets/thing/versions/7:access" {
		t.Errorf("full-form path = %q", gotPath)
	}
}

func TestGCPResolveMetadataToken(t *testing.T) {
	var apiAuth string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiAuth = r.Header.Get("Authorization")
		enc := base64.StdEncoding.EncodeToString([]byte("secret"))
		_, _ = io.WriteString(w, `{"payload":{"data":"`+enc+`"}}`)
	}))
	defer api.Close()

	var metaFlavor string
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metaFlavor = r.Header.Get("Metadata-Flavor")
		if r.URL.Path != "/computeMetadata/v1/instance/service-accounts/default/token" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"meta-token","expires_in":3599,"token_type":"Bearer"}`)
	}))
	defer meta.Close()

	r := GCPResolver{Project: "p", Endpoint: api.URL, MetadataBase: meta.URL, Client: &http.Client{}}
	got, err := r.Resolve(context.Background(), "gcp://name")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "secret" {
		t.Fatalf("value = %q", got)
	}
	if apiAuth != "Bearer meta-token" {
		t.Errorf("api Authorization = %q, want Bearer meta-token", apiAuth)
	}
	if metaFlavor != "Google" {
		t.Errorf("Metadata-Flavor = %q", metaFlavor)
	}
}

func TestGCPResolveMissingProject(t *testing.T) {
	r := GCPResolver{Token: "tok", Client: &http.Client{}}
	_, err := r.Resolve(context.Background(), "gcp://name")
	if err == nil || !strings.Contains(err.Error(), "GOOGLE_CLOUD_PROJECT") {
		t.Fatalf("expected project error, got %v", err)
	}
}

func TestGCPResolveNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"denied"}`)
	}))
	defer srv.Close()

	r := GCPResolver{Project: "p", Endpoint: srv.URL, Token: "tok", Client: &http.Client{}}
	_, err := r.Resolve(context.Background(), "gcp://name")
	if err == nil || !strings.Contains(err.Error(), "status 403") {
		t.Fatalf("expected non-200 error, got %v", err)
	}
}

func TestGCPResolveDecodeFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"payload":{"data":"!!!not-base64!!!"}}`)
	}))
	defer srv.Close()

	r := GCPResolver{Project: "p", Endpoint: srv.URL, Token: "tok", Client: &http.Client{}}
	_, err := r.Resolve(context.Background(), "gcp://name")
	if err == nil || !strings.Contains(err.Error(), "decode secret payload") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestParseGCPRef(t *testing.T) {
	r := GCPResolver{Project: "p"}
	tests := []struct {
		ref     string
		want    string
		wantErr bool
	}{
		{"gcp://name", "projects/p/secrets/name/versions/latest", false},
		{"gcp://name#4", "projects/p/secrets/name/versions/4", false},
		{"gcp://projects/x/secrets/y/versions/1", "projects/x/secrets/y/versions/1", false},
		{"gcp://", "", true},
		{"gcp://name#", "", true},
		{"gcp://projects/x/secrets/y", "", true},
		{"aws://name", "", true},
	}
	for _, tt := range tests {
		got, err := r.parseGCPRef(tt.ref)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseGCPRef(%q): expected error", tt.ref)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseGCPRef(%q): %v", tt.ref, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseGCPRef(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestParseGCPRefNoProjectShorthand(t *testing.T) {
	r := GCPResolver{}
	if _, err := r.parseGCPRef("gcp://name"); err == nil {
		t.Fatal("expected error when project unset for shorthand")
	}
}
