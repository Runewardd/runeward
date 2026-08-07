package cli

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Runewardd/runeward/internal/authz"
	"github.com/spf13/cobra"
)

type authRoundTripper func(*http.Request) (*http.Response, error)

func (f authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestAuthIsReservedCommand(t *testing.T) {
	got := rewriteForEnter([]string{"auth", "status"})
	if strings.Join(got, " ") != "auth status" {
		t.Fatalf("rewriteForEnter = %v", got)
	}
}

func TestDeviceFlowJSONErrorIsReturnedToPollingLoop(t *testing.T) {
	client := &http.Client{Transport: authRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(`{"error":"authorization_pending"}`)),
		}, nil
	})}
	req, err := http.NewRequest(http.MethodPost, "https://issuer.example/token", bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	var token deviceToken
	if err := doAuthJSON(client, req, &token); err != nil {
		t.Fatalf("authorization_pending should be decoded, got %v", err)
	}
	if token.Error != "authorization_pending" {
		t.Fatalf("error = %q", token.Error)
	}
}

func TestAuthEndpointRequiresTLSOutsideLoopback(t *testing.T) {
	if err := requireSecureAuthEndpoint("http://issuer.example"); err == nil {
		t.Fatal("expected insecure endpoint rejection")
	}
	if err := requireSecureAuthEndpoint("http://127.0.0.1:9090"); err != nil {
		t.Fatalf("loopback development endpoint rejected: %v", err)
	}
}

func TestNetworkListenersRequireTLS(t *testing.T) {
	t.Setenv(authz.EnvFile, "")
	t.Setenv(authz.EnvOIDCIssuer, "")
	config := t.TempDir()
	for _, tc := range []struct {
		name string
		cmd  func(*string) *cobra.Command
		args []string
	}{
		{"serve", newServeCmd, []string{"--bind", "0.0.0.0", "--token", strings.Repeat("x", 32)}},
		{"mcp", newMCPCmd, []string{"--http", "--bind", "0.0.0.0", "--token", strings.Repeat("x", 32)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := tc.cmd(&config)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), "plaintext") {
				t.Fatalf("expected plaintext refusal, got %v", err)
			}
		})
	}
}
