package webhook

import (
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestDecide(t *testing.T) {
	tests := []struct {
		name        string
		policies    []Policy
		namespace   string
		labels      map[string]string
		profile     string
		wantAllowed bool
		wantProfile string
	}{
		{
			name:        "no policies allows anything",
			policies:    nil,
			profile:     "anything",
			wantAllowed: true,
			wantProfile: "anything",
		},
		{
			name:        "allowed profile matches glob",
			policies:    []Policy{{AllowedProfiles: []string{"team-*"}}},
			profile:     "team-web",
			wantAllowed: true,
			wantProfile: "team-web",
		},
		{
			name:        "deny by denied profile",
			policies:    []Policy{{DeniedProfiles: []string{"*-root"}}},
			profile:     "prod-root",
			wantAllowed: false,
			wantProfile: "prod-root",
		},
		{
			name:        "deny when not in allowed list",
			policies:    []Policy{{AllowedProfiles: []string{"team-*"}}},
			profile:     "rogue",
			wantAllowed: false,
			wantProfile: "rogue",
		},
		{
			name:        "namespace restriction rejects",
			policies:    []Policy{{AllowedNamespaces: []string{"apps-*"}}},
			namespace:   "kube-system",
			profile:     "p",
			wantAllowed: false,
			wantProfile: "p",
		},
		{
			name:        "namespace restriction allows match",
			policies:    []Policy{{AllowedNamespaces: []string{"apps-*"}}},
			namespace:   "apps-prod",
			profile:     "p",
			wantAllowed: true,
			wantProfile: "p",
		},
		{
			name:        "required label missing rejects",
			policies:    []Policy{{RequiredLabels: []string{"team"}}},
			labels:      map[string]string{"env": "prod"},
			profile:     "p",
			wantAllowed: false,
			wantProfile: "p",
		},
		{
			name:        "required label present allows",
			policies:    []Policy{{RequiredLabels: []string{"team"}}},
			labels:      map[string]string{"team": "web"},
			profile:     "p",
			wantAllowed: true,
			wantProfile: "p",
		},
		{
			name:        "defaulting sets profile",
			policies:    []Policy{{DefaultProfile: "baseline"}},
			profile:     "",
			wantAllowed: true,
			wantProfile: "baseline",
		},
		{
			name: "defaulting then validated against allowed list",
			policies: []Policy{{
				DefaultProfile:  "baseline",
				AllowedProfiles: []string{"baseline", "team-*"},
			}},
			profile:     "",
			wantAllowed: true,
			wantProfile: "baseline",
		},
		{
			name: "first default wins across policies",
			policies: []Policy{
				{DefaultProfile: ""},
				{DefaultProfile: "second"},
			},
			profile:     "",
			wantAllowed: true,
			wantProfile: "second",
		},
		{
			name: "denied wins over allowed in same policy",
			policies: []Policy{{
				AllowedProfiles: []string{"team-*"},
				DeniedProfiles:  []string{"team-secret"},
			}},
			profile:     "team-secret",
			wantAllowed: false,
			wantProfile: "team-secret",
		},
		{
			name: "must satisfy every policy",
			policies: []Policy{
				{AllowedProfiles: []string{"team-*"}},
				{DeniedProfiles: []string{"team-legacy"}},
			},
			profile:     "team-legacy",
			wantAllowed: false,
			wantProfile: "team-legacy",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			allowed, profile, reason := Decide(tc.policies, tc.namespace, tc.labels, tc.profile)
			if allowed != tc.wantAllowed {
				t.Errorf("allowed = %v, want %v (reason=%q)", allowed, tc.wantAllowed, reason)
			}
			if profile != tc.wantProfile {
				t.Errorf("profile = %q, want %q", profile, tc.wantProfile)
			}
			if !allowed && reason == "" {
				t.Errorf("denied decision must include a reason")
			}
		})
	}
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"*", "anything", true},
		{"team-*", "team-web", true},
		{"team-*", "other", false},
		{"prod-?", "prod-1", true},
		{"prod-?", "prod-12", false},
		{"exact", "exact", true},
		{"exact", "exactly", false},
		{"a/*/c", "a/b/c", true},
	}
	for _, c := range cases {
		if got := matchGlob(c.pattern, c.s); got != c.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", c.pattern, c.s, got, c.want)
		}
	}
}

func TestProfilePatchNoAnnotations(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "sb"},
		"spec":     map[string]any{},
	}}
	patch, err := profilePatch(obj, "baseline")
	if err != nil {
		t.Fatalf("profilePatch: %v", err)
	}
	var ops []map[string]any
	if err := json.Unmarshal(patch, &ops); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops, got %d: %s", len(ops), patch)
	}
	if ops[0]["path"] != "/spec/profile" || ops[0]["value"] != "baseline" {
		t.Errorf("unexpected profile op: %v", ops[0])
	}
	if ops[1]["path"] != "/metadata/annotations" {
		t.Errorf("expected annotations map creation, got: %v", ops[1])
	}
}

func TestProfilePatchExistingAnnotations(t *testing.T) {
	obj := &unstructured.Unstructured{}
	obj.SetAnnotations(map[string]string{"existing": "1"})
	patch, err := profilePatch(obj, "baseline")
	if err != nil {
		t.Fatalf("profilePatch: %v", err)
	}
	var ops []map[string]any
	if err := json.Unmarshal(patch, &ops); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	want := "/metadata/annotations/runeward.dev~1cluster-policy-defaulted"
	if ops[1]["path"] != want {
		t.Errorf("annotation path = %v, want %v", ops[1]["path"], want)
	}
}

func TestGenerateCertBlocks(t *testing.T) {
	if _, _, _, err := GenerateCert(nil); err != nil {
		t.Fatalf("GenerateCert(nil): %v", err)
	}
}

func TestGenerateCert(t *testing.T) {
	certPEM, keyPEM, caPEM, err := GenerateCert([]string{"svc.ns.svc", "svc.ns.svc.cluster.local"})
	if err != nil {
		t.Fatalf("GenerateCert: %v", err)
	}
	for name, b := range map[string][]byte{"cert": certPEM, "key": keyPEM, "ca": caPEM} {
		if len(b) == 0 {
			t.Errorf("%s PEM is empty", name)
		}
	}
}
