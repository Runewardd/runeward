package secrets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnvResolver(t *testing.T) {
	ctx := context.Background()
	r := EnvResolver{}

	t.Run("set", func(t *testing.T) {
		t.Setenv("RUNEWARD_TEST_SECRET", "hunter2")
		got, err := r.Resolve(ctx, "env://RUNEWARD_TEST_SECRET")
		if err != nil {
			t.Fatalf("Resolve: unexpected error: %v", err)
		}
		if got != "hunter2" {
			t.Fatalf("Resolve = %q, want %q", got, "hunter2")
		}
	})

	t.Run("unset", func(t *testing.T) {
		// Ensure the var is absent for this subtest.
		t.Setenv("RUNEWARD_TEST_SECRET", "")
		if _, err := r.Resolve(ctx, "env://RUNEWARD_TEST_SECRET"); err == nil {
			t.Fatal("Resolve: expected error for unset/empty variable, got nil")
		}
	})

	t.Run("missing name", func(t *testing.T) {
		if _, err := r.Resolve(ctx, "env://"); err == nil {
			t.Fatal("Resolve: expected error for missing variable name, got nil")
		}
	})
}

// fakeVault serves a KV v2 response for one path and 404 for everything else,
// recording the token header it was called with.
func fakeVault(t *testing.T, wantPath string, body map[string]any) (*httptest.Server, *string) {
	t.Helper()
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotToken = req.Header.Get("X-Vault-Token")
		if req.URL.Path != wantPath {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &gotToken
}

func TestVaultResolver(t *testing.T) {
	ctx := context.Background()
	body := map[string]any{
		"data": map[string]any{
			"data": map[string]any{"password": "s3cret"},
		},
	}
	srv, gotToken := fakeVault(t, "/v1/kv/data/database/prod", body)

	r := VaultResolver{Addr: srv.URL, Token: "test-token", Client: srv.Client()}

	t.Run("resolves field and sends token", func(t *testing.T) {
		got, err := r.Resolve(ctx, "vault://kv/database/prod#password")
		if err != nil {
			t.Fatalf("Resolve: unexpected error: %v", err)
		}
		if got != "s3cret" {
			t.Fatalf("Resolve = %q, want %q", got, "s3cret")
		}
		if *gotToken != "test-token" {
			t.Fatalf("X-Vault-Token = %q, want %q", *gotToken, "test-token")
		}
	})

	t.Run("missing field", func(t *testing.T) {
		if _, err := r.Resolve(ctx, "vault://kv/database/prod#username"); err == nil {
			t.Fatal("Resolve: expected error for missing field, got nil")
		}
	})

	t.Run("404", func(t *testing.T) {
		if _, err := r.Resolve(ctx, "vault://kv/does/not/exist#password"); err == nil {
			t.Fatal("Resolve: expected error for 404, got nil")
		}
	})

	t.Run("missing addr", func(t *testing.T) {
		bad := VaultResolver{Addr: "", Token: "test-token", Client: srv.Client()}
		if _, err := bad.Resolve(ctx, "vault://kv/database/prod#password"); err == nil {
			t.Fatal("Resolve: expected error for unset VAULT_ADDR, got nil")
		}
	})

	t.Run("missing token", func(t *testing.T) {
		bad := VaultResolver{Addr: srv.URL, Token: "", Client: srv.Client()}
		if _, err := bad.Resolve(ctx, "vault://kv/database/prod#password"); err == nil {
			t.Fatal("Resolve: expected error for unset VAULT_TOKEN, got nil")
		}
	})

	t.Run("malformed refs", func(t *testing.T) {
		for _, ref := range []string{
			"vault://kv/database/prod", // no #field
			"vault://kv/database/prod#",
			"vault://kv#password", // no path
			"vault://#password",
		} {
			if _, err := r.Resolve(ctx, ref); err == nil {
				t.Fatalf("Resolve(%q): expected error, got nil", ref)
			}
		}
	})
}

func TestDispatcher(t *testing.T) {
	ctx := context.Background()

	t.Run("routes env", func(t *testing.T) {
		t.Setenv("RUNEWARD_TEST_SECRET", "routed")
		got, err := Default().Resolve(ctx, "env://RUNEWARD_TEST_SECRET")
		if err != nil {
			t.Fatalf("Resolve: unexpected error: %v", err)
		}
		if got != "routed" {
			t.Fatalf("Resolve = %q, want %q", got, "routed")
		}
	})

	t.Run("op is not built in", func(t *testing.T) {
		_, err := Default().Resolve(ctx, "op://vault/item/field")
		if err == nil {
			t.Fatal("Resolve: expected error for op://, got nil")
		}
		if !strings.Contains(err.Error(), "1Password") {
			t.Fatalf("Resolve error = %q, want it to mention 1Password", err)
		}
	})

	t.Run("unknown scheme", func(t *testing.T) {
		if _, err := Default().Resolve(ctx, "gopher://x/y"); err == nil {
			t.Fatal("Resolve: expected error for unknown scheme, got nil")
		}
	})

	t.Run("no scheme", func(t *testing.T) {
		if _, err := Default().Resolve(ctx, "just-a-literal"); err == nil {
			t.Fatal("Resolve: expected error for missing scheme, got nil")
		}
	})
}
