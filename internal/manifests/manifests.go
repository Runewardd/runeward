// Package manifests embeds the runeward Kubernetes install artifacts (CRDs and
// the controller install bundle) so `runeward up` can apply them without any
// external files. The same YAML is the canonical source for the copies shipped
// under deploy/ (kept in sync by a drift test).
package manifests

import (
	"embed"
	"io/fs"
	"sort"
)

//go:embed crds/*.yaml install/*.yaml
var files embed.FS

// CRDs returns the CustomResourceDefinition documents, sorted by filename.
func CRDs() ([][]byte, error) { return read("crds") }

// Install returns the namespace/RBAC/Deployment documents that make up the
// controller install bundle, sorted by filename (numeric prefixes order them).
func Install() ([][]byte, error) { return read("install") }

func read(dir string) ([][]byte, error) {
	entries, err := fs.ReadDir(files, dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	out := make([][]byte, 0, len(names))
	for _, n := range names {
		b, err := files.ReadFile(dir + "/" + n)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}
