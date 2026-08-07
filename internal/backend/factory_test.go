package backend

import (
	"reflect"
	"testing"

	"github.com/Runewardd/runeward/internal/profile"
)

func TestSpecFromProfilePropagatesCommand(t *testing.T) {
	p := &profile.Profile{
		Name: "browser",
		Host: profile.Host{
			Image:   "example/browser:1",
			Command: []string{"/bin/sh", "-c", "sleep infinity"},
		},
	}
	spec := SpecFromProfile(p, nil)
	if !reflect.DeepEqual(spec.Command, p.Host.Command) {
		t.Fatalf("command = %#v, want %#v", spec.Command, p.Host.Command)
	}
}
