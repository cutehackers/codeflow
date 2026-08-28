package fusion

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Canonical layer enum for contract (flowspec steps layer).
const (
	LayerPresentation = "presentation"
	LayerController   = "controller"
	LayerUsecase      = "usecase"
	LayerDomain       = "domain"
	LayerData         = "data"
	LayerInfra        = "infra"
	LayerExternal     = "external"
	LayerUnknown      = "unknown"
)

// CanonicalLayerOrder is the normative lane order from spec §8.3.
var CanonicalLayerOrder = []string{
	LayerPresentation,
	LayerController,
	LayerUsecase,
	LayerDomain,
	LayerData,
	LayerInfra,
	LayerExternal,
	LayerUnknown,
}

// LayerOrder maps canonical layer → sort rank (unknown last).
var LayerOrder = map[string]int{
	LayerPresentation: 0,
	LayerController:   1,
	LayerUsecase:      2,
	LayerDomain:       3,
	LayerData:         4,
	LayerInfra:        5,
	LayerExternal:     6,
	LayerUnknown:      99,
}

// LayersConfig mirrors codeflow.layers.yaml (§4.1.3).
type LayersConfig struct {
	Version           int        `yaml:"version" json:"version"`
	Layers            []LayerDef `yaml:"layers" json:"layers"`
	StrictOrder       bool       `yaml:"strictOrder" json:"strictOrder"`
	AllowUnknownLayer bool       `yaml:"allowUnknownLayer" json:"allowUnknownLayer"`
}

// LayerDef is one entry in codeflow.layers.yaml.
type LayerDef struct {
	Name         string   `yaml:"name" json:"name"`
	Aliases      []string `yaml:"aliases" json:"aliases"`
	PathPatterns []string `yaml:"pathPatterns" json:"pathPatterns"`
}

// built-in alias table used when codeflow.layers.yaml is absent.
var builtinAliasToCanonical = map[string]string{
	"ui":           LayerPresentation,
	"view":         LayerPresentation,
	"widget":       LayerPresentation,
	"page":         LayerPresentation,
	"screen":       LayerPresentation,
	"dialog":       LayerPresentation,
	"presentation": LayerPresentation,
	"controller":   LayerController,
	"notifier":     LayerController,
	"bloc":         LayerController,
	"cubit":        LayerController,
	"application":  LayerUsecase,
	"service":      LayerUsecase,
	"interactor":   LayerUsecase,
	"use_case":     LayerUsecase,
	"usecase":      LayerUsecase,
	"domain":       LayerDomain,
	"entity":       LayerDomain,
	"repository":   LayerData,
	"datasource":   LayerData,
	"data_source":  LayerData,
	"data":         LayerData,
	"infrastructure": LayerInfra,
	"platform":       LayerInfra,
	"infra":          LayerInfra,
	"api":            LayerExternal,
	"remote":         LayerExternal,
	"gateway":        LayerExternal,
	"client":         LayerExternal,
	"external":       LayerExternal,
	"network":        LayerExternal,
	"http":           LayerExternal,
}

// LoadLayersConfig reads <repoRoot>/codeflow.layers.yaml when present.
// Missing file → returns default config (8 canonical layers, StrictOrder true, AllowUnknown false) with no error.
// Parse or validation error → returns error with code layers_config_invalid semantics.
func LoadLayersConfig(repoRoot string) (*LayersConfig, error) {
	path := filepath.Join(repoRoot, "codeflow.layers.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultLayersConfig(), nil
		}
		return nil, fmt.Errorf("read codeflow.layers.yaml: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return defaultLayersConfig(), nil
	}
	var cfg LayersConfig
	// Default strictness before decode: true for StrictOrder, false for AllowUnknown (spec default)
	cfg.StrictOrder = true
	cfg.AllowUnknownLayer = false
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("layers_config_invalid: parse codeflow.layers.yaml: %w", err)
	}
	if cfg.Version != 1 {
		return nil, fmt.Errorf("layers_config_invalid: version must be 1, got %d", cfg.Version)
	}
	if len(cfg.Layers) == 0 {
		return nil, fmt.Errorf("layers_config_invalid: layers must not be empty")
	}
	seen := map[string]bool{}
	for i, ld := range cfg.Layers {
		canonical := strings.ToLower(strings.TrimSpace(ld.Name))
		if _, ok := LayerOrder[canonical]; !ok {
			return nil, fmt.Errorf("layers_config_invalid: layers[%d].name %q is not a canonical layer", i, ld.Name)
		}
		if seen[canonical] {
			return nil, fmt.Errorf("layers_config_invalid: duplicate layer name %q", canonical)
		}
		seen[canonical] = true
		cfg.Layers[i].Name = canonical
		// Normalize aliases to lower
		for j, a := range ld.Aliases {
			cfg.Layers[i].Aliases[j] = strings.ToLower(strings.TrimSpace(a))
		}
	}
	return &cfg, nil
}

func defaultLayersConfig() *LayersConfig {
	// Built-in: 7 canonical without unknown, with builtin aliases and no path patterns.
	// Reverse builtinAliasToCanonical into canonical → aliases list.
	aliasesByCanonical := map[string][]string{}
	for alias, canon := range builtinAliasToCanonical {
		// Skip self-mapping (e.g., "presentation" → "presentation") to avoid duplicate alias equals name.
		if alias == canon {
			continue
		}
		aliasesByCanonical[canon] = append(aliasesByCanonical[canon], alias)
	}
	layers := make([]LayerDef, 0, 7)
	for _, name := range CanonicalLayerOrder {
		if name == LayerUnknown {
			continue
		}
		ld := LayerDef{Name: name}
		if aliases, ok := aliasesByCanonical[name]; ok {
			// Sort for deterministic order
			sorted := make([]string, len(aliases))
			copy(sorted, aliases)
			sort.Strings(sorted)
			ld.Aliases = sorted
		}
		layers = append(layers, ld)
	}
	return &LayersConfig{
		Version:           1,
		Layers:            layers,
		StrictOrder:       true,
		AllowUnknownLayer: false,
	}
}

// NormalizeLayer canonicalizes a raw layer string via LayersConfig or builtin table.
// Returns canonical name and unknown=true when no mapping exists.
// Spec §4.1.3: lower-cases, trims, takes last segment after '/'.
func NormalizeLayer(raw string, cfg *LayersConfig) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return LayerUnknown, true
	}
	// Take last segment after '/'
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		s = s[idx+1:]
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return LayerUnknown, true
	}
	// Direct canonical match
	if _, ok := LayerOrder[s]; ok && s != LayerUnknown {
		return s, false
	}
	// If cfg present (file-defined aliases), search there first
	if cfg != nil {
		for _, ld := range cfg.Layers {
			if s == ld.Name {
				return ld.Name, false
			}
			for _, alias := range ld.Aliases {
				if s == strings.ToLower(strings.TrimSpace(alias)) {
					return ld.Name, false
				}
			}
		}
		// cfg present but not found → unknown
		return LayerUnknown, true
	}
	// Fallback builtin alias table when cfg is nil/absent
	if canon, ok := builtinAliasToCanonical[s]; ok {
		return canon, false
	}
	// Also try direct canonical check for unknown itself
	if s == LayerUnknown {
		return LayerUnknown, false
	}
	return LayerUnknown, true
}

// ValidateLayerOrder validates monotonic layer progression for steps.
// If declaredLayers is non-empty, it is the authoritative order (artifact.layers).
// Otherwise order is inferred from first occurrence (no error, only used for render, but we still detect backward hops if StrictOrder).
// branchKind == "branch" allows backward hops.
// When StrictOrder==false, backward hops become warnings (returned as warnings, not errors).
// Returns warnings (unknowns-style entries should be built by caller) and error for strict violations.
func ValidateLayerOrder(steps []struct {
	Layer string
	Kind  string
}, declaredLayers []string, cfg *LayersConfig) (warnings []string, err error) {
	strict := true
	if cfg != nil {
		strict = cfg.StrictOrder
	}
	orderIndex := map[string]int{}
	if len(declaredLayers) > 0 {
		for i, l := range declaredLayers {
			canon, unk := NormalizeLayer(l, cfg)
			if unk {
				// declared layer itself unknown → validation error when strict
				if strict {
					return nil, fmt.Errorf("layer_order_violation: declared layers contains unknown %q", l)
				}
				continue
			}
			if _, ok := orderIndex[canon]; !ok {
				orderIndex[canon] = i
			}
		}
	} else {
		// Infer order from first occurrence
		nextIdx := 0
		for _, st := range steps {
			canon, unk := NormalizeLayer(st.Layer, cfg)
			if unk {
				canon = LayerUnknown
			}
			if _, ok := orderIndex[canon]; !ok {
				orderIndex[canon] = nextIdx
				nextIdx++
			}
		}
	}
	// Ensure every canonical in declared set has an index; steps not in declared set get append order
	// For steps, map canonical to order index; if not in declared set, treat as after all declared (strict violation only if backward)
	maxIdx := len(orderIndex)
	for _, st := range steps {
		canon, unk := NormalizeLayer(st.Layer, cfg)
		if unk {
			canon = LayerUnknown
		}
		if _, ok := orderIndex[canon]; !ok {
			orderIndex[canon] = maxIdx
			maxIdx++
		}
	}
	// Walk steps in ordinal order and check monotonicity
	prevIdx := -1
	for i, st := range steps {
		canon, unk := NormalizeLayer(st.Layer, cfg)
		if unk {
			canon = LayerUnknown
		}
		curIdx := orderIndex[canon]
		if curIdx < prevIdx {
			if st.Kind == "branch" {
				// branch exception: allowed
				continue
			}
			msg := fmt.Sprintf("layer_order_violation at ordinal %d: %q (%d) goes backward after %d", i+1, st.Layer, curIdx, prevIdx)
			if strict {
				return nil, fmt.Errorf("%s", msg)
			}
			warnings = append(warnings, msg)
		}
		if curIdx > prevIdx {
			prevIdx = curIdx
		}
	}
	return warnings, nil
}

// ValidatePathPatterns checks each step's repoRelativePath against the pathPatterns for its canonical layer.
// Returns warnings for mismatches; never errors by itself (advisory). Caller may combine with layer order check.
func ValidatePathPatterns(steps []struct {
	Layer            string
	RepoRelativePath string
}, cfg *LayersConfig) []string {
	if cfg == nil {
		return nil
	}
	// Build layer → patterns
	patternsByLayer := map[string][]string{}
	for _, ld := range cfg.Layers {
		if len(ld.PathPatterns) > 0 {
			patternsByLayer[ld.Name] = ld.PathPatterns
		}
	}
	var warnings []string
	for i, st := range steps {
		canon, unk := NormalizeLayer(st.Layer, cfg)
		if unk {
			continue
		}
		pats := patternsByLayer[canon]
		if len(pats) == 0 {
			continue
		}
		matched := false
		for _, pat := range pats {
			if matchDoublestar(st.RepoRelativePath, pat) {
				matched = true
				break
			}
		}
		if !matched {
			warnings = append(warnings, fmt.Sprintf("step %d layer %q path %q does not match any pathPatterns %v", i+1, canon, st.RepoRelativePath, pats))
		}
	}
	return warnings
}

// matchDoublestar matches path against a doublestar glob like "**/presentation/**".
// Supports **, *, ?, character classes are not needed for this spec.
func matchDoublestar(path, pattern string) bool {
	// Normalize to forward slashes
	path = filepath.ToSlash(path)
	pattern = filepath.ToSlash(pattern)
	// Convert pattern to regex
	reStr := "^"
	i := 0
	for i < len(pattern) {
		c := pattern[i]
		if c == '*' {
			// Check for **
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				// **
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					// **/  → (.*/)?   (zero or more dirs with trailing slash)
					reStr += "(.*/)?"
					i += 3
				} else {
					// ** at end or without slash → .*
					reStr += ".*"
					i += 2
				}
			} else {
				// * → [^/]*
				reStr += "[^/]*"
				i++
			}
		} else if c == '?' {
			reStr += "[^/]"
			i++
		} else if strings.ContainsRune(`.+^$()[]{}|\`, rune(c)) {
			reStr += "\\" + string(c)
			i++
		} else {
			reStr += string(c)
			i++
		}
	}
	reStr += "$"
	// Compile and match
	re, err := regexp.Compile(reStr)
	if err != nil {
		// Fallback to simple string contains
		return strings.Contains(path, strings.Trim(pattern, "*"))
	}
	return re.MatchString(path)
}

// LayerIndex returns LayerOrder index for canonical layer, or 99 for unknown.
func LayerIndex(layer string) int {
	if idx, ok := LayerOrder[strings.ToLower(strings.TrimSpace(layer))]; ok {
		return idx
	}
	return 99
}

// SortedLayers returns canonical layers sorted by LayerOrder.
func SortedLayers(layers []string) []string {
	out := make([]string, len(layers))
	copy(out, layers)
	sort.Slice(out, func(i, j int) bool {
		return LayerIndex(out[i]) < LayerIndex(out[j])
	})
	return out
}
