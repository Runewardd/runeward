// Package credentials stores the current Runeward client login separately
// from server-side authz configuration.
package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const EnvFile = "RUNEWARD_CREDENTIALS_FILE"

type Login struct {
	Token     string    `json:"token"`
	Issuer    string    `json:"issuer,omitempty"`
	ClientID  string    `json:"client_id,omitempty"`
	Audience  string    `json:"audience,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

func Path() (string, error) {
	if path := strings.TrimSpace(os.Getenv(EnvFile)); path != "" {
		return path, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "runeward", "credentials.json"), nil
}

func Load() (Login, error) {
	path, err := Path()
	if err != nil {
		return Login{}, err
	}
	if info, err := os.Stat(path); runtime.GOOS != "windows" && err == nil && info.Mode().Perm()&0o077 != 0 {
		return Login{}, fmt.Errorf("credentials file %s must use 0600 permissions", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Login{}, err
	}
	var login Login
	if err := json.Unmarshal(data, &login); err != nil {
		return Login{}, err
	}
	if strings.TrimSpace(login.Token) == "" {
		return Login{}, errors.New("stored Runeward credential is empty")
	}
	return login, nil
}

func LoadToken() string {
	login, err := Load()
	if err != nil || (!login.ExpiresAt.IsZero() && time.Now().UTC().After(login.ExpiresAt)) {
		return ""
	}
	return login.Token
}

func Save(login Login) error {
	login.Token = strings.TrimSpace(login.Token)
	if login.Token == "" {
		return errors.New("cannot save an empty Runeward credential")
	}
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(login, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".credentials-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func Delete() error {
	path, err := Path()
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
