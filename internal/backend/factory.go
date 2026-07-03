package backend

import (
	"fmt"

	"github.com/adefemi171/runeward/internal/profile"
)

// For returns the backend implementing the profile's execution host.
func For(p *profile.Profile) (Backend, error) {
	switch p.Host.Type {
	case profile.HostContainer, "":
		return NewDocker()
	case profile.HostK8s:
		return NewK8s()
	default:
		return nil, fmt.Errorf("no backend for host.type %q", p.Host.Type)
	}
}

// SpecFromProfile derives a backend-agnostic Spec from a resolved profile.
// Env values are expected to already be resolved to literals by the caller.
func SpecFromProfile(p *profile.Profile, env map[string]string) Spec {
	return Spec{
		Profile:      p.Name,
		Image:        p.Host.Image,
		Workdir:      p.Host.Workdir,
		User:         p.Host.User,
		Env:          env,
		Files:        p.Files,
		SeedDir:      expandHome(p.Host.CopyFrom),
		Network:      p.Network,
		RuntimeClass: p.Host.RuntimeClass,
		Labels: map[string]string{
			labelProfile: p.Name,
		},
	}
}
