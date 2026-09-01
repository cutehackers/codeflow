package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectArchitecturePattern_FSD(t *testing.T) {
	cases := []struct {
		name        string
		directories []string
		files       map[string]string
	}{
		{
			name:        "src/features and src/entities",
			directories: []string{"src/features/auth", "src/entities/user", "src/shared"},
			files: map[string]string{
				"package.json": `{"name":"fsd-app","dependencies":{"react":"18.2.0"}}`,
			},
		},
		{
			name:        "root features and entities",
			directories: []string{"features/cart", "entities/product", "pages/home"},
			files: map[string]string{
				"package.json": `{"name":"fsd-root-app"}`,
			},
		},
		{
			name:        "src/widgets and src/features",
			directories: []string{"src/widgets/header", "src/features/login"},
			files: map[string]string{
				"package.json": `{"name":"fsd-widget-app"}`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, d := range tc.directories {
				if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", d, err)
				}
			}
			for f, content := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte(content), 0o644); err != nil {
					t.Fatalf("writeFile %s: %v", f, err)
				}
			}

			pattern, err := DetectArchitecturePattern(dir)
			if err != nil {
				t.Fatalf("DetectArchitecturePattern failed: %v", err)
			}
			if pattern != PatternFeatureSlicedDesign {
				t.Errorf("got pattern %q, want %q", pattern, PatternFeatureSlicedDesign)
			}
		})
	}
}

func TestDetectArchitecturePattern_NextAppRouter(t *testing.T) {
	cases := []struct {
		name        string
		directories []string
		files       map[string]string
	}{
		{
			name:        "next dependency with app directory",
			directories: []string{"app/login", "app/api/auth", "components"},
			files: map[string]string{
				"package.json": `{"name":"next-app","dependencies":{"next":"14.2.0","react":"18.2.0"}}`,
				"app/layout.tsx": "export default function Layout() {}",
				"app/page.tsx":   "export default function Page() {}",
			},
		},
		{
			name:        "next.config.mjs with src/app",
			directories: []string{"src/app", "src/components"},
			files: map[string]string{
				"package.json":    `{"name":"next-app-src"}`,
				"next.config.mjs": "export default {}",
				"src/app/page.tsx": "export default function Page() {}",
			},
		},
		{
			name:        "app router file without package.json",
			directories: []string{"app"},
			files: map[string]string{
				"app/page.tsx": "export default function Home() {}",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, d := range tc.directories {
				if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", d, err)
				}
			}
			for f, content := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte(content), 0o644); err != nil {
					t.Fatalf("writeFile %s: %v", f, err)
				}
			}

			pattern, err := DetectArchitecturePattern(dir)
			if err != nil {
				t.Fatalf("DetectArchitecturePattern failed: %v", err)
			}
			if pattern != PatternNextAppRouter {
				t.Errorf("got pattern %q, want %q", pattern, PatternNextAppRouter)
			}
		})
	}
}

func TestDetectArchitecturePattern_ReactSPA(t *testing.T) {
	cases := []struct {
		name        string
		directories []string
		files       map[string]string
	}{
		{
			name:        "react dependency with components and hooks",
			directories: []string{"src/components", "src/hooks", "src/pages"},
			files: map[string]string{
				"package.json": `{"name":"spa-app","dependencies":{"react":"18.2.0","react-dom":"18.2.0"}}`,
			},
		},
		{
			name:        "zustand state management with stores",
			directories: []string{"src/stores", "src/views"},
			files: map[string]string{
				"package.json": `{"name":"spa-zustand","dependencies":{"zustand":"4.5.0"}}`,
			},
		},
		{
			name:        "vite config with components",
			directories: []string{"src/components"},
			files: map[string]string{
				"vite.config.ts": "import { defineConfig } from 'vite';",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, d := range tc.directories {
				if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", d, err)
				}
			}
			for f, content := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte(content), 0o644); err != nil {
					t.Fatalf("writeFile %s: %v", f, err)
				}
			}

			pattern, err := DetectArchitecturePattern(dir)
			if err != nil {
				t.Fatalf("DetectArchitecturePattern failed: %v", err)
			}
			if pattern != PatternStandardReactSPA {
				t.Errorf("got pattern %q, want %q", pattern, PatternStandardReactSPA)
			}
		})
	}
}

func TestDetectArchitecturePattern_CleanArchitecture(t *testing.T) {
	cases := []struct {
		name        string
		directories []string
		files       map[string]string
	}{
		{
			name:        "Flutter Dart project",
			directories: []string{"lib/presentation", "lib/domain", "lib/data"},
			files: map[string]string{
				"pubspec.yaml": "name: flutter_app\n",
			},
		},
		{
			name:        "Go project",
			directories: []string{"cmd/app", "internal/domain", "internal/usecases"},
			files: map[string]string{
				"go.mod": "module example.com/app\ngo 1.22\n",
			},
		},
		{
			name:        "explicit clean architecture directories",
			directories: []string{"domain", "usecases", "repositories", "controllers"},
			files:       map[string]string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, d := range tc.directories {
				if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", d, err)
				}
			}
			for f, content := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte(content), 0o644); err != nil {
					t.Fatalf("writeFile %s: %v", f, err)
				}
			}

			pattern, err := DetectArchitecturePattern(dir)
			if err != nil {
				t.Fatalf("DetectArchitecturePattern failed: %v", err)
			}
			if pattern != PatternCleanArchitecture {
				t.Errorf("got pattern %q, want %q", pattern, PatternCleanArchitecture)
			}
		})
	}
}

func TestDetectArchitecturePattern_GenericFrontendAndFallbacks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"generic-tool"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	pattern, err := DetectArchitecturePattern(dir)
	if err != nil {
		t.Fatalf("DetectArchitecturePattern failed: %v", err)
	}
	if pattern != PatternGenericFrontend {
		t.Errorf("got pattern %q, want %q", pattern, PatternGenericFrontend)
	}
}

func TestDetectArchitecturePattern_NonExistentDir(t *testing.T) {
	_, err := DetectArchitecturePattern(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestInspectPackageJSON(t *testing.T) {
	dir := t.TempDir()
	pkgContent := `{
		"name": "my-pkg",
		"dependencies": {"next": "14.0.0", "react": "18.2.0"},
		"devDependencies": {"typescript": "^5.0.0"}
	}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgContent), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest, err := InspectPackageJSON(dir)
	if err != nil {
		t.Fatalf("InspectPackageJSON failed: %v", err)
	}
	if manifest.Name != "my-pkg" {
		t.Errorf("Name = %q, want 'my-pkg'", manifest.Name)
	}
	if !manifest.HasDependency("next") || !manifest.HasDependency("typescript") {
		t.Errorf("HasDependency check failed")
	}
	if manifest.HasDependency("lodash") {
		t.Errorf("HasDependency returned true for non-existent dependency")
	}
	if !manifest.HasAnyDependency("vue", "react") {
		t.Errorf("HasAnyDependency failed")
	}

	// Missing package.json
	emptyDir := t.TempDir()
	if _, err := InspectPackageJSON(emptyDir); err == nil {
		t.Error("expected error for missing package.json")
	}

	// Corrupted package.json
	corruptDir := t.TempDir()
	os.WriteFile(filepath.Join(corruptDir, "package.json"), []byte(`{invalid`), 0o644)
	if _, err := InspectPackageJSON(corruptDir); err == nil {
		t.Error("expected error for corrupt package.json")
	}
}
