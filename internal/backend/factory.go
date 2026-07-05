package backend

import (
	"fmt"

	"github.com/Runewardd/runeward/internal/profile"
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
		ReadOnly:     p.Host.ReadOnly,
		Seccomp:      p.Host.Seccomp,
		AppArmor:     p.Host.AppArmor,
		Resources:    resourcesFromLimits(p.Limits),
		Labels: map[string]string{
			labelProfile: p.Name,
		},
	}
}

// resourcesFromLimits maps a profile's declared CPU/memory limits onto backend
// resource caps. Previously these limits were parsed but never applied.
func resourcesFromLimits(l profile.Limits) Resources {
	var r Resources
	if l.Memory != "" {
		if b, ok := parseSize(l.Memory); ok {
			r.MemoryBytes = b
		}
	}
	if l.CPUs > 0 {
		r.NanoCPUs = int64(l.CPUs * 1e9)
	}
	return r
}
