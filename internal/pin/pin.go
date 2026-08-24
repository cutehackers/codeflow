// Package pin resolves the adapter versions pinned by this core release.
//
// The pin table ships embedded (compatibility.json, format inherited from v1
// but reshaped for v2) and follows design-v2 §11.2: a core release declares
// which adapter versions it is compatible with; init only records the pinned
// version. Parse is the override hook for tests.
package pin

import (
	"embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

//go:embed compatibility.json
var compatibilityFS embed.FS

const compatibilityFileName = "compatibility.json"

// ExpectedSchemaVersion is the compatibility-file schema this package reads.
const ExpectedSchemaVersion = "2.0"

// Adapter is one adapter's compatibility entry.
type Adapter struct {
	Pinned string `json:"pinned"`
	Min    string `json:"min"`
}

type compatibilityFile struct {
	SchemaVersion string             `json:"schemaVersion"`
	Adapters      map[string]Adapter `json:"adapters"`
}

// Registry resolves adapter names to their pinned versions.
type Registry struct {
	adapters map[string]Adapter
}

// Default returns the registry backed by the embedded compatibility data.
func Default() (*Registry, error) {
	data, err := compatibilityFS.ReadFile(compatibilityFileName)
	if err != nil {
		return nil, fmt.Errorf("read embedded %s: %w", compatibilityFileName, err)
	}
	return Parse(data)
}

// Parse builds a Registry from raw compatibility bytes. It doubles as the
// test override hook: tests construct registries with arbitrary data instead
// of relying on the embedded release table.
func Parse(data []byte) (*Registry, error) {
	var file compatibilityFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse compatibility data: %w", err)
	}
	if file.SchemaVersion != ExpectedSchemaVersion {
		return nil, fmt.Errorf("compatibility schema %q unsupported (want %q)", file.SchemaVersion, ExpectedSchemaVersion)
	}
	if len(file.Adapters) == 0 {
		return nil, fmt.Errorf("compatibility data declares no adapters")
	}
	for name, adapter := range file.Adapters {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("compatibility data has an empty adapter name")
		}
		if strings.TrimSpace(adapter.Pinned) == "" {
			return nil, fmt.Errorf("adapter %s has no pinned version", name)
		}
		if strings.TrimSpace(adapter.Min) == "" {
			return nil, fmt.Errorf("adapter %s has no minimum version", name)
		}
	}
	return &Registry{adapters: file.Adapters}, nil
}

// Resolve returns the pinned version for an adapter name.
func (r *Registry) Resolve(name string) (string, error) {
	adapter, ok := r.adapters[name]
	if !ok {
		return "", fmt.Errorf("no pin for unknown adapter %q", name)
	}
	return adapter.Pinned, nil
}

// Min returns the minimum compatible version for an adapter name.
func (r *Registry) Min(name string) (string, error) {
	adapter, ok := r.adapters[name]
	if !ok {
		return "", fmt.Errorf("no pin for unknown adapter %q", name)
	}
	return adapter.Min, nil
}

// Pins returns a copy of all pinned versions keyed by adapter name.
func (r *Registry) Pins() map[string]string {
	out := make(map[string]string, len(r.adapters))
	for name, adapter := range r.adapters {
		out[name] = adapter.Pinned
	}
	return out
}

// Names lists adapter names in sorted order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.adapters))
	for name := range r.adapters {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
