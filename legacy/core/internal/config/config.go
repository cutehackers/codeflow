// Package config reads the small, intentionally strict CodeFlow project contract.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const SupportedSchemaVersion = "1"

type Config struct {
	SchemaVersion string             `yaml:"schema_version"`
	Repository    RepositoryConfig   `yaml:"repository"`
	Analysis      AnalysisConfig     `yaml:"analysis"`
	Features      map[string]Feature `yaml:"features"`
}

type RepositoryConfig struct {
	ID string `yaml:"id"`
}

type AnalysisConfig struct {
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude"`
}

type Feature struct {
	EntryPoint string `yaml:"entry_point"`
}

type Result struct {
	Present  bool
	Config   Config
	Warnings []string
}

var aliasPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// Load reads codeflow.yaml without creating or changing any project files.
func Load(repo string) (Result, error) {
	path := filepath.Join(repo, "codeflow.yaml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Result{}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("read codeflow.yaml: %w", err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return Result{Present: true}, fmt.Errorf("parse codeflow.yaml: %w", err)
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return Result{Present: true}, fmt.Errorf("codeflow.yaml must contain a mapping")
	}
	warnings := unknownFields(root.Content[0])

	var cfg Config
	if err := root.Decode(&cfg); err != nil {
		return Result{Present: true}, fmt.Errorf("decode codeflow.yaml: %w", err)
	}
	if cfg.SchemaVersion == "" {
		return Result{Present: true}, fmt.Errorf("schema_version is required; use %q", SupportedSchemaVersion)
	}
	if cfg.SchemaVersion != SupportedSchemaVersion {
		return Result{Present: true}, fmt.Errorf("unsupported schema_version %q; supported version is %q", cfg.SchemaVersion, SupportedSchemaVersion)
	}
	if cfg.Repository.ID == "" {
		return Result{Present: true}, fmt.Errorf("repository.id is required when codeflow.yaml is present")
	}
	for name, feature := range cfg.Features {
		if !aliasPattern.MatchString(name) {
			return Result{Present: true}, fmt.Errorf("features.%s is not a valid alias", name)
		}
		if !validLogicalEntryPoint(feature.EntryPoint) {
			return Result{Present: true}, fmt.Errorf("features.%s.entry_point %q is not a supported exact logical entry point", name, feature.EntryPoint)
		}
	}
	for _, glob := range append(append([]string{}, cfg.Analysis.Include...), cfg.Analysis.Exclude...) {
		if strings.TrimSpace(glob) == "" {
			return Result{Present: true}, fmt.Errorf("analysis include and exclude patterns must not be empty")
		}
	}
	sort.Strings(warnings)
	return Result{Present: true, Config: cfg, Warnings: warnings}, nil
}

func validLogicalEntryPoint(value string) bool {
	if strings.HasPrefix(value, "route:/") || strings.HasPrefix(value, "system:") {
		return !strings.ContainsAny(value, " \t\n") && ((strings.HasPrefix(value, "route:/") && len(value) > len("route:/")) || (strings.HasPrefix(value, "system:") && len(value) > len("system:")))
	}
	// Symbol and handler entry points are exact canonical identifiers, not aliases.
	for _, prefix := range []string{"symbol:", "handler:"} {
		if strings.HasPrefix(value, prefix) {
			return len(value) > len(prefix) && !strings.ContainsAny(value, " \t\n")
		}
	}
	return false
}

func unknownFields(mapping *yaml.Node) []string {
	known := map[string]map[string]bool{
		"":           {"schema_version": true, "repository": true, "analysis": true, "features": true},
		"repository": {"id": true},
		"analysis":   {"include": true, "exclude": true},
		"feature":    {"entry_point": true},
	}
	var warnings []string
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key, value := mapping.Content[i].Value, mapping.Content[i+1]
		if !known[""][key] {
			warnings = append(warnings, fmt.Sprintf("unknown configuration field %q is ignored", key))
			continue
		}
		var allowed map[string]bool
		switch key {
		case "repository", "analysis":
			allowed = known[key]
			if value.Kind == yaml.MappingNode {
				for j := 0; j+1 < len(value.Content); j += 2 {
					if !allowed[value.Content[j].Value] {
						warnings = append(warnings, fmt.Sprintf("unknown configuration field %s.%s is ignored", key, value.Content[j].Value))
					}
				}
			}
		case "features":
			if value.Kind == yaml.MappingNode {
				for j := 0; j+1 < len(value.Content); j += 2 {
					featureName, feature := value.Content[j].Value, value.Content[j+1]
					if feature.Kind == yaml.MappingNode {
						for k := 0; k+1 < len(feature.Content); k += 2 {
							if !known["feature"][feature.Content[k].Value] {
								warnings = append(warnings, fmt.Sprintf("unknown configuration field features.%s.%s is ignored", featureName, feature.Content[k].Value))
							}
						}
					}
				}
			}
		}
	}
	return warnings
}
