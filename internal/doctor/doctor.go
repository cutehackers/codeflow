// Package doctor provides diagnostic checks for the codeflow environment,
// workspace integrity, adapter readiness, and contract schemas (ticket 19).
package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"codeflow/internal/contractharness"
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

	// 2. Check Dart SDK
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

	// 3. Check Dart adapter
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
