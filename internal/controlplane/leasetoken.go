package controlplane

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Runewardd/runeward/internal/fleet"
)

const leaseKeyFileName = "cohort-lease.key"

var ErrInvalidLease = errors.New("cohort: invalid or expired task lease")

type leaseClaims struct {
	Version int    `json:"v"`
	Cohort  string `json:"cohort"`
	Task    string `json:"task"`
	Actor   string `json:"actor"`
	Expires int64  `json:"exp"`
	Lease   int    `json:"lease"`
	Nonce   string `json:"nonce"`
}

func loadOrCreateLeaseKey(stateDir string) ([]byte, error) {
	if stateDir == "" {
		return nil, errors.New("cohort lease signing requires a state directory")
	}
	path := filepath.Join(stateDir, leaseKeyFileName)
	if key, err := os.ReadFile(path); err == nil {
		if info, statErr := os.Stat(path); runtime.GOOS != "windows" && statErr == nil && info.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("cohort lease key %s must use 0600 permissions", path)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("cohort lease key %s has invalid length", path)
		}
		return key, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read cohort lease key: %w", err)
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create cohort lease state directory: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate cohort lease key: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return loadOrCreateLeaseKey(stateDir)
		}
		return nil, fmt.Errorf("create cohort lease key: %w", err)
	}
	if _, err := f.Write(key); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write cohort lease key: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("sync cohort lease key: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close cohort lease key: %w", err)
	}
	return key, nil
}

func (m *Manager) issueTaskLease(cohortID string, task fleet.Task) (string, error) {
	expires := task.LeaseExpiry
	if expires.IsZero() {
		expires = time.Now().UTC().Add(24 * time.Hour)
	}
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", err
	}
	claims := leaseClaims{
		Version: 1, Cohort: cohortID, Task: task.ID, Actor: task.Owner,
		Expires: expires.Unix(), Lease: task.LeaseVersion,
		Nonce: base64.RawURLEncoding.EncodeToString(nonceBytes),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, m.leaseKey)
	_, _ = mac.Write([]byte(body))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return body + "." + sig, nil
}

func (m *Manager) verifyTaskLease(token, cohortID, taskID, actor string, leaseVersion int) error {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ErrInvalidLease
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ErrInvalidLease
	}
	mac := hmac.New(sha256.New, m.leaseKey)
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return ErrInvalidLease
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ErrInvalidLease
	}
	var claims leaseClaims
	if json.Unmarshal(payload, &claims) != nil || claims.Version != 1 ||
		claims.Cohort != cohortID || claims.Task != taskID || claims.Actor != actor ||
		claims.Lease != leaseVersion || claims.Nonce == "" || time.Now().UTC().Unix() >= claims.Expires {
		return ErrInvalidLease
	}
	return nil
}
