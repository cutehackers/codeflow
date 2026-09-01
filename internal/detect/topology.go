// Package detect identifies project languages and architectural topologies.
package detect

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ArchitecturePattern defines recognized project structural patterns.
type ArchitecturePattern string

const (
	PatternFeatureSlicedDesign ArchitecturePattern = "fsd"
	PatternNextAppRouter       ArchitecturePattern = "nextjs_app"
	PatternStandardReactSPA    ArchitecturePattern = "react_spa"
	PatternCleanArchitecture   ArchitecturePattern = "clean_arch"
	PatternGenericFrontend     ArchitecturePattern = "generic_fe"
)

// PackageManifest represents relevant fields from package.json for topology detection.
type PackageManifest struct {
	Name            string            `json:"name"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Scripts         map[string]string `json:"scripts"`
}

// HasDependency checks if a package exists in dependencies or devDependencies.
func (m *PackageManifest) HasDependency(name string) bool {
	if m == nil {
		return false
	}
	if _, ok := m.Dependencies[name]; ok {
		return true
	}
	if _, ok := m.DevDependencies[name]; ok {
		return true
	}
	return false
}

// HasAnyDependency checks if any of the given packages exist in dependencies or devDependencies.
func (m *PackageManifest) HasAnyDependency(names ...string) bool {
	for _, name := range names {
		if m.HasDependency(name) {
			return true
		}
	}
	return false
}

// InspectPackageJSON reads and parses package.json from the repository root.
func InspectPackageJSON(root string) (*PackageManifest, error) {
	pkgPath := filepath.Join(root, "package.json")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return nil, err
	}
	var manifest PackageManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse package.json: %w", err)
	}
	return &manifest, nil
}

// DetectArchitecturePattern inspects directory hierarchy and package dependencies
// to determine the project's architectural pattern.
func DetectArchitecturePattern(root string) (ArchitecturePattern, error) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("detect architecture: directory not accessible: %s", root)
	}

	manifest, _ := InspectPackageJSON(root)

	// 1. Feature-Sliced Design (FSD)
	// Strong signals: features + entities directories (in root or src/), or widgets + features/entities.
	hasFsdSlices := (dirExists(root, "src/features") || dirExists(root, "features")) &&
		(dirExists(root, "src/entities") || dirExists(root, "entities"))
	hasWidgets := (dirExists(root, "src/widgets") || dirExists(root, "widgets")) &&
		(dirExists(root, "src/features") || dirExists(root, "features") || dirExists(root, "src/pages") || dirExists(root, "pages"))

	if hasFsdSlices || hasWidgets {
		return PatternFeatureSlicedDesign, nil
	}

	// 2. Next.js App Router
	// Strong signals: next dependency/config with app/ or src/app/ directory, or Next.js App Router files.
	hasNextDep := manifest != nil && manifest.HasDependency("next")
	hasNextConfig := fileExists(root, "next.config.js") || fileExists(root, "next.config.mjs") || fileExists(root, "next.config.ts")
	hasAppDir := dirExists(root, "app") || dirExists(root, "src/app")

	if (hasNextDep || hasNextConfig) && hasAppDir {
		return PatternNextAppRouter, nil
	}
	if hasAppRouterFiles(root) {
		return PatternNextAppRouter, nil
	}
	if (hasNextDep || hasNextConfig) && (dirExists(root, "pages") || dirExists(root, "src/pages")) {
		return PatternNextAppRouter, nil
	}

	// 3. Standard React SPA
	// Signals: React/SPA dependencies or typical SPA folder structure (components, hooks, stores, contexts, views).
	hasReactDep := manifest != nil && manifest.HasAnyDependency(
		"react", "react-dom", "@reduxjs/toolkit", "zustand", "recoil", "mobx",
		"@tanstack/react-query", "vue", "svelte",
	)
	hasSpaDirs := dirExists(root, "src/components") || dirExists(root, "components") ||
		dirExists(root, "src/hooks") || dirExists(root, "hooks") ||
		dirExists(root, "src/contexts") || dirExists(root, "contexts") ||
		dirExists(root, "src/stores") || dirExists(root, "stores") ||
		dirExists(root, "src/views") || dirExists(root, "views")
	hasViteConfig := fileExists(root, "vite.config.ts") || fileExists(root, "vite.config.js") || fileExists(root, "vite.config.mjs")

	if hasReactDep || (hasSpaDirs && manifest != nil) || hasViteConfig {
		return PatternStandardReactSPA, nil
	}

	// 4. Clean Architecture
	// Signals: Flutter/Dart (pubspec.yaml), Go (go.mod), or explicit Clean Architecture folders.
	isDart := fileExists(root, "pubspec.yaml")
	isGo := fileExists(root, "go.mod")
	hasCleanDirs := dirExists(root, "domain") || dirExists(root, "usecases") || dirExists(root, "usecase") ||
		dirExists(root, "controllers") || dirExists(root, "repositories") ||
		dirExists(root, "lib/features") || dirExists(root, "lib/presentation")

	if isDart || isGo || hasCleanDirs {
		return PatternCleanArchitecture, nil
	}

	// 5. Generic Frontend Fallback
	if manifest != nil || fileExists(root, "tsconfig.json") || dirExists(root, "src") {
		return PatternGenericFrontend, nil
	}

	// Default fallback
	return PatternCleanArchitecture, nil
}

func dirExists(root, subPath string) bool {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(subPath)))
	return err == nil && info.IsDir()
}

func fileExists(root, subPath string) bool {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(subPath)))
	return err == nil && !info.IsDir()
}

func hasAppRouterFiles(root string) bool {
	appRouterCandidates := []string{
		"app/page.tsx", "app/page.jsx", "app/page.js", "app/page.ts",
		"app/layout.tsx", "app/layout.jsx", "app/layout.js", "app/layout.ts",
		"app/route.ts", "app/route.js",
		"src/app/page.tsx", "src/app/page.jsx", "src/app/page.js", "src/app/page.ts",
		"src/app/layout.tsx", "src/app/layout.jsx", "src/app/layout.js", "src/app/layout.ts",
		"src/app/route.ts", "src/app/route.js",
	}
	for _, candidate := range appRouterCandidates {
		if fileExists(root, candidate) {
			return true
		}
	}
	return false
}
