package credentials

import (
	"os"
	"runtime"
	"testing"
	"time"
)

func TestSaveLoadDelete(t *testing.T) {
	path := t.TempDir() + "/credentials.json"
	t.Setenv(EnvFile, path)
	want := Login{Token: "secret", Issuer: "https://issuer.example", ExpiresAt: time.Now().Add(time.Hour).UTC().Truncate(time.Second)}
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != want.Token || got.Issuer != want.Issuer || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
	if LoadToken() != want.Token {
		t.Fatal("LoadToken did not return the saved valid token")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("credential permissions = %v", info.Mode().Perm())
		}
	}
	if err := Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); !os.IsNotExist(err) {
		t.Fatalf("expected removed credential, got %v", err)
	}
}

func TestExpiredAndEmptyCredentialsAreNotUsable(t *testing.T) {
	t.Setenv(EnvFile, t.TempDir()+"/credentials.json")
	if err := Save(Login{Token: "expired", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if token := LoadToken(); token != "" {
		t.Fatalf("expired token returned: %q", token)
	}
	if err := Save(Login{}); err == nil {
		t.Fatal("expected empty credential rejection")
	}
}
