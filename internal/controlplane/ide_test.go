package controlplane

import (
	"context"
	"testing"

	"github.com/Runewardd/runeward/internal/backend"
	"github.com/Runewardd/runeward/internal/profile"
)

func TestExperimentalIDEFlag(t *testing.T) {
	t.Setenv("RUNEWARD_ENABLE_EXPERIMENTAL_IDE", "")
	if ExperimentalIDEEnabled() {
		t.Fatal("expected disabled")
	}
	t.Setenv("RUNEWARD_ENABLE_EXPERIMENTAL_IDE", "1")
	if !ExperimentalIDEEnabled() {
		t.Fatal("expected enabled")
	}
}

func TestExperimentalIDEReadinessFlag(t *testing.T) {
	t.Setenv("RUNEWARD_ENABLE_EXPERIMENTAL_IDE", "")
	check, ready := experimentalIDEReadiness()
	if ready || check.Status != "fail" {
		t.Fatalf("disabled IDE readiness = %v, %q; want false, fail", ready, check.Status)
	}

	t.Setenv("RUNEWARD_ENABLE_EXPERIMENTAL_IDE", "1")
	check, ready = experimentalIDEReadiness()
	if !ready || check.Status != "ok" {
		t.Fatalf("enabled IDE readiness = %v, %q; want true, ok", ready, check.Status)
	}
}

func TestIDEEndpointLifecycle(t *testing.T) {
	m, _ := newTestManager(t, nil, 0)
	m.InjectSession(&Session{
		Sandbox: &backend.Sandbox{ID: "s1", Profile: "p", Status: "running"},
		Profile: &profile.Profile{Name: "p", IDE: profile.IDE{Enabled: true, Port: 8080}},
	})
	if _, ok := m.IDEEndpoint("s1"); ok {
		t.Fatal("endpoint should be empty before set")
	}
	m.SetIDEEndpointForTest("s1", "10.0.0.2:8080")
	ep, ok := m.IDEEndpoint("s1")
	if !ok || ep != "10.0.0.2:8080" {
		t.Fatalf("endpoint = %q,%v", ep, ok)
	}
	m.recordIDEClose(context.Background(), m.sessions["s1"])
}
