// Package doctor provides diagnostic checks for the codeflow environment,
// workspace integrity, adapter readiness, and contract schemas (ticket 19).
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"codeflow/internal/analyzer/detect"
	"codeflow/internal/analyzer/protocol"
	"codeflow/internal/analyzer/workspace"
	"codeflow/schemas"
)

// StorageChecker checks generation pointer and index for repoRoot.
type StorageChecker func(repoRoot string) []CheckResult

// SchemaChecker checks contract schemas compilation.
type SchemaChecker func() CheckResult

// AdapterResolver resolves adapter configuration for a language and spec.
type AdapterResolver func(lang string, spec string) (protocol.Config, error)

var (
	defaultAdapterResolver AdapterResolver = protocol.ResolveAdapter
	defaultStorageChecker  StorageChecker
	defaultSchemaChecker   SchemaChecker
)

// RegisterAdapterResolver configures the adapter resolver used by doctor.
func RegisterAdapterResolver(fn AdapterResolver) {
	defaultAdapterResolver = fn
}

// RegisterStorageChecker configures the storage diagnostics provider.
func RegisterStorageChecker(fn StorageChecker) {
	defaultStorageChecker = fn
}

// RegisterSchemaChecker configures the schema compilation diagnostics provider.
func RegisterSchemaChecker(fn SchemaChecker) {
	defaultSchemaChecker = fn
}

// CheckResult represents one diagnostic item result.
type CheckResult struct {
	Name    string
	Passed  bool
	Message string
}

// DiagnoseAdapter verifies that an adapter can complete CORE's protocol
// initialization. Resolving a binary path alone does not prove compatibility.
func DiagnoseAdapter(name string, cfg protocol.Config) CheckResult {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := protocol.Spawn(ctx, cfg)
	if err != nil {
		return CheckResult{
			Name:    name,
			Passed:  false,
			Message: fmt.Sprintf("Adapter protocol check failed: %v", err),
		}
	}
	version := conn.Version()
	_ = conn.Close()
	return CheckResult{
		Name:    name,
		Passed:  true,
		Message: fmt.Sprintf("Adapter ready (%s, protocol v%d)", filepath.Base(cfg.BinPath), version.ProtocolVersion),
	}
}

// Diagnose runs all health checks against repoRoot.
func Diagnose(repoRoot string, dartAdapterSpec string) []CheckResult {
	var results []CheckResult

	// 1. Check workspace initialized
	wsPath := workspace.FilePath(repoRoot)
	if _, err := os.Stat(wsPath); err == nil {
		results = append(results, CheckResult{
			Name:    "Workspace manifest",
			Passed:  true,
			Message: fmt.Sprintf("Found %s", wsPath),
		})
	} else {
		results = append(results, CheckResult{
			Name:    "Workspace manifest",
			Passed:  false,
			Message: "Workspace not initialized (run 'codeflow init')",
		})
	}

	// 2. Detect Project Language
	det := detect.Detect(repoRoot)

	switch det.Language {
	case "typescript", "javascript":
		// Node.js toolchain check (Node.js 18+ recommended)
		if nodePath, err := exec.LookPath("node"); err == nil {
			verOutput, _ := exec.Command(nodePath, "--version").Output()
			verStr := strings.TrimSpace(string(verOutput))
			results = append(results, CheckResult{
				Name:    "Node.js Runtime",
				Passed:  true,
				Message: fmt.Sprintf("Found node %s at %s", verStr, nodePath),
			})
		} else {
			results = append(results, CheckResult{
				Name:    "Node.js Runtime",
				Passed:  false,
				Message: "node executable not found in PATH (Node.js 18+ required)",
			})
		}

		// TypeScript adapter check
		cfg, err := defaultAdapterResolver("typescript", "")
		if err == nil {
			results = append(results, DiagnoseAdapter("TypeScript adapter", cfg))
		} else {
			results = append(results, CheckResult{
				Name:    "TypeScript adapter",
				Passed:  false,
				Message: fmt.Sprintf("Adapter resolution error: %v", err),
			})
		}

		// TypeScript Language Server (LSP) check: local project .bin first, then global
		localLsp := filepath.Join(repoRoot, "node_modules", ".bin", "typescript-language-server")
		if _, err := os.Stat(localLsp); err == nil {
			results = append(results, CheckResult{
				Name:    "TypeScript LSP (Optional)",
				Passed:  true,
				Message: fmt.Sprintf("Found project-local LSP at %s", localLsp),
			})
		} else if globalLsp, err := exec.LookPath("typescript-language-server"); err == nil {
			results = append(results, CheckResult{
				Name:    "TypeScript LSP (Optional)",
				Passed:  true,
				Message: fmt.Sprintf("Found global LSP at %s", globalLsp),
			})
		} else {
			results = append(results, CheckResult{
				Name:    "TypeScript LSP (Optional)",
				Passed:  true,
				Message: "Not found (local AST parser active; optional: npx typescript-language-server)",
			})
		}

	case "kotlin", "java":
		if javaPath, err := exec.LookPath("java"); err == nil {
			results = append(results, CheckResult{
				Name:    "Java/JVM Runtime",
				Passed:  true,
				Message: fmt.Sprintf("Found java at %s", javaPath),
			})
		} else {
			results = append(results, CheckResult{
				Name:    "Java/JVM Runtime",
				Passed:  false,
				Message: "java executable not found in PATH",
			})
		}
		cfg, err := defaultAdapterResolver("kotlin", "")
		if err == nil {
			results = append(results, DiagnoseAdapter("Kotlin adapter", cfg))
		} else {
			results = append(results, CheckResult{
				Name:    "Kotlin adapter",
				Passed:  false,
				Message: fmt.Sprintf("Adapter resolution error: %v", err),
			})
		}

	case "swift":
		if swiftPath, err := exec.LookPath("swift"); err == nil {
			results = append(results, CheckResult{
				Name:    "Swift Toolchain",
				Passed:  true,
				Message: fmt.Sprintf("Found swift at %s", swiftPath),
			})
		} else {
			results = append(results, CheckResult{
				Name:    "Swift Toolchain",
				Passed:  false,
				Message: "swift executable not found in PATH",
			})
		}
		cfg, err := defaultAdapterResolver("swift", "")
		if err == nil {
			results = append(results, DiagnoseAdapter("Swift adapter", cfg))
		} else {
			results = append(results, CheckResult{
				Name:    "Swift adapter",
				Passed:  false,
				Message: fmt.Sprintf("Adapter resolution error: %v", err),
			})
		}

	case "python":
		pyPath, err := exec.LookPath("python3")
		if err != nil {
			pyPath, err = exec.LookPath("python")
		}
		if err == nil {
			results = append(results, CheckResult{
				Name:    "Python Runtime",
				Passed:  true,
				Message: fmt.Sprintf("Found python at %s", pyPath),
			})
		} else {
			results = append(results, CheckResult{
				Name:    "Python Runtime",
				Passed:  false,
				Message: "python executable not found in PATH",
			})
		}
		cfg, err := defaultAdapterResolver("python", "")
		if err == nil {
			results = append(results, DiagnoseAdapter("Python adapter", cfg))
		} else {
			results = append(results, CheckResult{
				Name:    "Python adapter",
				Passed:  false,
				Message: fmt.Sprintf("Adapter resolution error: %v", err),
			})
		}

	case "go":
		if goPath, err := exec.LookPath("go"); err == nil {
			results = append(results, CheckResult{
				Name:    "Go Toolchain",
				Passed:  true,
				Message: fmt.Sprintf("Found go at %s", goPath),
			})
		} else {
			results = append(results, CheckResult{
				Name:    "Go Toolchain",
				Passed:  false,
				Message: "go executable not found in PATH",
			})
		}

	case "rust":
		if cargoPath, err := exec.LookPath("cargo"); err == nil {
			results = append(results, CheckResult{
				Name:    "Rust Toolchain",
				Passed:  true,
				Message: fmt.Sprintf("Found cargo at %s", cargoPath),
			})
		} else {
			results = append(results, CheckResult{
				Name:    "Rust Toolchain",
				Passed:  false,
				Message: "cargo executable not found in PATH",
			})
		}

	default: // Dart (or unconfident / general)
		if dartPath, err := exec.LookPath("dart"); err == nil {
			results = append(results, CheckResult{
				Name:    "Dart SDK",
				Passed:  true,
				Message: fmt.Sprintf("Found dart at %s", dartPath),
			})
		} else {
			results = append(results, CheckResult{
				Name:    "Dart SDK",
				Passed:  false,
				Message: "dart SDK not found in PATH",
			})
		}

		// Dart adapter check
		cfg, err := defaultAdapterResolver("dart", dartAdapterSpec)
		if err == nil {
			results = append(results, DiagnoseAdapter("Dart adapter", cfg))
		} else {
			results = append(results, CheckResult{
				Name:    "Dart adapter",
				Passed:  false,
				Message: fmt.Sprintf("Adapter resolution error: %v", err),
			})
		}
	}

	// 4 & 5. Check generation pointer & index
	if defaultStorageChecker != nil {
		results = append(results, defaultStorageChecker(repoRoot)...)
	} else {
		// Fallback: inspect generation files directly without storage package dependency
		genDir := filepath.Join(repoRoot, ".codeflow", "generations")
		ptrFile := filepath.Join(genDir, "current.json")
		if data, err := os.ReadFile(ptrFile); err == nil {
			var ptr struct {
				GenerationID string    `json:"generationId"`
				FlowCount    int       `json:"flowCount"`
				PublishedAt  time.Time `json:"publishedAt"`
			}
			if err := json.Unmarshal(data, &ptr); err == nil {
				results = append(results, CheckResult{
					Name:    "Generation pointer",
					Passed:  true,
					Message: fmt.Sprintf("Generation %s (%d flows) %s", ptr.GenerationID, ptr.FlowCount, ptr.PublishedAt.Format("2006-01-02 15:04")),
				})
				idxFile := filepath.Join(genDir, ptr.GenerationID, "index.json")
				if idxData, err := os.ReadFile(idxFile); err == nil {
					var idx struct {
						GenerationID string `json:"generationId"`
						Flows        []any  `json:"flows"`
					}
					if err := json.Unmarshal(idxData, &idx); err == nil {
						results = append(results, CheckResult{
							Name:    "Generation index",
							Passed:  true,
							Message: fmt.Sprintf("Index %s with %d flows", idx.GenerationID, len(idx.Flows)),
						})
					} else {
						results = append(results, CheckResult{Name: "Generation index", Passed: false, Message: "pointer exists but index missing"})
					}
				} else {
					results = append(results, CheckResult{Name: "Generation index", Passed: false, Message: "pointer exists but index missing"})
				}
			} else {
				results = append(results, CheckResult{Name: "Generation pointer", Passed: false, Message: fmt.Sprintf("read pointer failed: %v", err)})
			}
		} else {
			results = append(results, CheckResult{Name: "Generation pointer", Passed: true, Message: "No generation published yet (run 'codeflow publish')"})
		}
	}

	// 6. Check contract schemas compile
	if defaultSchemaChecker != nil {
		results = append(results, defaultSchemaChecker())
	} else {
		// Fallback: verify embedded contract schemas directly without collector dependency
		entries, err := fs.ReadDir(schemas.FS, ".")
		if err != nil {
			results = append(results, CheckResult{Name: "Contract schemas", Passed: false, Message: fmt.Sprintf("read embedded schemas failed: %v", err)})
		} else {
			count := 0
			var parseErr error
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".schema.json") {
					data, err := schemas.FS.ReadFile(entry.Name())
					if err != nil {
						parseErr = err
						break
					}
					var raw json.RawMessage
					if err := json.Unmarshal(data, &raw); err != nil {
						parseErr = fmt.Errorf("%s: %w", entry.Name(), err)
						break
					}
					count++
				}
			}
			if parseErr != nil {
				results = append(results, CheckResult{Name: "Contract schemas", Passed: false, Message: fmt.Sprintf("schema parse failed: %v", parseErr)})
			} else {
				results = append(results, CheckResult{Name: "Contract schemas", Passed: true, Message: fmt.Sprintf("All %d schemas verified", count)})
			}
		}
	}

	return results
}
