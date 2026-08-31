// Package doctor provides diagnostic checks for the codeflow environment,
// workspace integrity, adapter readiness, and contract schemas (ticket 19).
package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"codeflow/internal/contractharness"
	"codeflow/internal/detect"
	"codeflow/internal/harvest"
	"codeflow/internal/storage"
	"codeflow/internal/workspace"
)

// CheckResult represents one diagnostic item result.
type CheckResult struct {
	Name    string
	Passed  bool
	Message string
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
		// Node.js toolchain check
		if nodePath, err := exec.LookPath("node"); err == nil {
			results = append(results, CheckResult{
				Name:    "Node.js Runtime",
				Passed:  true,
				Message: fmt.Sprintf("Found node at %s", nodePath),
			})
		} else {
			results = append(results, CheckResult{
				Name:    "Node.js Runtime",
				Passed:  false,
				Message: "node executable not found in PATH",
			})
		}

		// TypeScript adapter check
		cfg, err := harvest.ResolveAdapter("typescript", "")
		if err == nil {
			results = append(results, CheckResult{
				Name:    "TypeScript adapter",
				Passed:  true,
				Message: fmt.Sprintf("Adapter ready (%s)", filepath.Base(cfg.BinPath)),
			})
		} else {
			results = append(results, CheckResult{
				Name:    "TypeScript adapter",
				Passed:  false,
				Message: fmt.Sprintf("Adapter resolution error: %v", err),
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
		cfg, err := harvest.ResolveAdapter("kotlin", "")
		if err == nil {
			results = append(results, CheckResult{
				Name:    "Kotlin adapter",
				Passed:  true,
				Message: fmt.Sprintf("Adapter ready (%s)", filepath.Base(cfg.BinPath)),
			})
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
		cfg, err := harvest.ResolveAdapter("swift", "")
		if err == nil {
			results = append(results, CheckResult{
				Name:    "Swift adapter",
				Passed:  true,
				Message: fmt.Sprintf("Adapter ready (%s)", filepath.Base(cfg.BinPath)),
			})
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
		cfg, err := harvest.ResolveAdapter("python", "")
		if err == nil {
			results = append(results, CheckResult{
				Name:    "Python adapter",
				Passed:  true,
				Message: fmt.Sprintf("Adapter ready (%s)", filepath.Base(cfg.BinPath)),
			})
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
		cfg, err := harvest.ResolveDartAdapter(dartAdapterSpec)
		if err == nil {
			results = append(results, CheckResult{
				Name:    "Dart adapter",
				Passed:  true,
				Message: fmt.Sprintf("Adapter ready (%s)", filepath.Base(cfg.BinPath)),
			})
		} else {
			results = append(results, CheckResult{
				Name:    "Dart adapter",
				Passed:  false,
				Message: fmt.Sprintf("Adapter resolution error: %v", err),
			})
		}
	}

	// 4. Check generation pointer
	st := storage.New(repoRoot)
	ptr, err := st.ReadPointer()
	if err != nil {
		results = append(results, CheckResult{Name: "Generation pointer", Passed: false, Message: fmt.Sprintf("read pointer failed: %v", err)})
	} else if ptr == nil {
		results = append(results, CheckResult{Name: "Generation pointer", Passed: true, Message: "No generation published yet (run 'codeflow publish')"})
	} else {
		results = append(results, CheckResult{Name: "Generation pointer", Passed: true, Message: fmt.Sprintf("Generation %s (%d flows) %s", ptr.GenerationID, ptr.FlowCount, ptr.PublishedAt.Format("2006-01-02 15:04"))})
		// 5. Check latest index
		idx, err := st.ReadLatestIndex()
		if err != nil {
			results = append(results, CheckResult{Name: "Generation index", Passed: false, Message: fmt.Sprintf("read index failed: %v", err)})
		} else if idx == nil {
			results = append(results, CheckResult{Name: "Generation index", Passed: false, Message: "pointer exists but index missing"})
		} else {
			results = append(results, CheckResult{Name: "Generation index", Passed: true, Message: fmt.Sprintf("Index %s with %d flows", idx.GenerationID, len(idx.Flows))})
		}
	}

	// 6. Check contract schemas compile
	if err := contractharness.EnsureAllCompiled(); err != nil {
		results = append(results, CheckResult{Name: "Contract schemas", Passed: false, Message: fmt.Sprintf("schema compile failed: %v", err)})
	} else {
		results = append(results, CheckResult{Name: "Contract schemas", Passed: true, Message: "All 6 schemas compiled"})
	}

	return results
}
