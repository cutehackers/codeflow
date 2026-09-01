package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/fusion"
	"codeflow/internal/harvest"
	"codeflow/internal/protocol"
	"codeflow/internal/slicing"
)

// moduleRoot returns the absolute path to the repository root.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine source location")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// fixtureDir returns the absolute path to a fixture directory in test/fixtures.
func fixtureDir(t *testing.T, fixtureName string) string {
	t.Helper()
	path := filepath.Join(moduleRoot(t), "test", "fixtures", fixtureName)
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("fixture directory %s not found: %v", fixtureName, err)
	}
	return path
}

// copyDir recursively copies a directory tree from src to dst.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// makeTempCopy creates a temporary directory with a full copy of the fixture repo.
func makeTempCopy(t *testing.T, fixtureName string) string {
	t.Helper()
	src := fixtureDir(t, fixtureName)
	temp := t.TempDir()
	target := filepath.Join(temp, fixtureName)
	if err := copyDir(src, target); err != nil {
		t.Fatalf("failed to copy fixture %s: %v", fixtureName, err)
	}
	return target
}

// tsAdapterPool creates an active protocol pool to the TypeScript adapter.
func tsAdapterPool(t *testing.T) (*protocol.Pool, context.Context, context.CancelFunc) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not found: %v", err)
	}
	root := moduleRoot(t)
	spec := "noderun:" + filepath.Join(root, "adapters", "typescript")
	cfg, err := harvest.ResolveAdapter("typescript", spec)
	if err != nil {
		t.Fatalf("ResolveAdapter failed: %v", err)
	}

	pool := protocol.NewPool(cfg, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	return pool, ctx, cancel
}

// validateContract validates an arbitrary JSON-serializable value against a contract schema.
func validateContract(t *testing.T, schemaFileName string, instance any) {
	t.Helper()
	data, err := json.Marshal(instance)
	if err != nil {
		t.Fatalf("json.Marshal failed for %s: %v", schemaFileName, err)
	}
	schemaID := contractharness.BaseURL + schemaFileName
	if err := contractharness.Validate(schemaID, data); err != nil {
		t.Fatalf("Contract violation against %s:\nInstance JSON:\n%s\nError:\n%v", schemaFileName, string(data), err)
	}
}

// sha256Hex calculates SHA-256 in lowercase hex.
func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// computeCanonicalAstFingerprint calculates the canonical AST fingerprint for a code snippet.
func computeCanonicalAstFingerprint(code string) string {
	// Strip single-line comments
	reSingle := regexp.MustCompile(`//[^\n]*`)
	s := reSingle.ReplaceAllString(code, "")
	// Strip multi-line comments
	reMulti := regexp.MustCompile(`/\*[\s\S]*?\*/`)
	s = reMulti.ReplaceAllString(s, "")
	// Normalize whitespace
	reWS := regexp.MustCompile(`\s+`)
	s = strings.TrimSpace(reWS.ReplaceAllString(s, " "))
	return sha256Hex([]byte(s))
}

// validateSliceStepLayers wraps fusion.ValidateLayerOrder with SliceStep slice.
func validateSliceStepLayers(steps []slicing.SliceStep, declaredLayers []string, cfg *fusion.LayersConfig) (warnings []string, err error) {
	var rawSteps []struct {
		Layer string
		Kind  string
	}
	for _, s := range steps {
		rawSteps = append(rawSteps, struct {
			Layer string
			Kind  string
		}{
			Layer: s.Layer,
			Kind:  s.Kind,
		})
	}
	if len(declaredLayers) == 0 {
		declaredLayers = []string{
			fusion.LayerPresentation,
			fusion.LayerController,
			fusion.LayerUsecase,
			fusion.LayerDomain,
			fusion.LayerData,
			fusion.LayerInfra,
			fusion.LayerExternal,
		}
	}
	return fusion.ValidateLayerOrder(rawSteps, declaredLayers, cfg)
}

// sliceHelper executes the slice operation over the protocol pool with required parameters.
func sliceHelper(t *testing.T, pool *protocol.Pool, ctx context.Context, repoRoot, filePath, symbolName string, depth int) (slicing.SlicedPayload, error) {
	t.Helper()
	var payload slicing.SlicedPayload
	entryPath := filePath
	if symbolName != "" {
		if !strings.Contains(filePath, "#") {
			entryPath = filePath + "#" + symbolName
		}
	}
	err := pool.Call(ctx, "slice", map[string]any{
		"repoRoot":        repoRoot,
		"candidateId":     "cand-0000000000000001",
		"entrySymbolPath": entryPath,
		"opts": map[string]any{
			"maxDepth": depth,
		},
	}, &payload)
	return payload, err
}

// assertNoViolations checks that ValidateLayerOrder returns nil.
func assertNoViolations(t *testing.T, err error, contextMsg string) {
	t.Helper()
	if err != nil {
		t.Errorf("expected 0 layer_order_violation errors in %s, got: %v", contextMsg, err)
	}
}
