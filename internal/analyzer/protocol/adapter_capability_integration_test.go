package protocol

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"codeflow/internal/collector/contractharness"
	"codeflow/internal/collector/evidence"
)

// TestSupportedAdapterInitializeCapabilityMeasurements derives the capability
// matrix from initialize responses and an executable snapshot probe for each
// supported adapter. A static language map cannot make this test pass.
func TestSupportedAdapterInitializeCapabilityMeasurements(t *testing.T) {
	root := repoRootDir(t)
	configs := map[string]Config{}
	if node, err := exec.LookPath("node"); err == nil {
		configs["typescript"] = Config{BinPath: node, Args: []string{filepath.Join(root, "adapters", "typescript", "bin", "codeflow_ts_adapter.js")}}
	} else {
		t.Fatalf("node runtime required for TypeScript capability measurement: %v", err)
	}
	if dart, err := exec.LookPath("dart"); err == nil {
		configs["dart"] = Config{BinPath: dart, Args: []string{"run", filepath.Join(root, "adapters", "dart", "bin", "codeflow_dart_adapter.dart")}}
	} else {
		t.Fatalf("dart runtime required for Dart capability measurement: %v", err)
	}
	goAdapter := provideGoAdapterBinary(t)
	configs["go"] = Config{BinPath: goAdapter}

	registry := NewAdapterRegistry(1)
	for _, adapter := range []string{"dart", "typescript", "go"} {
		adapter, cfg := adapter, configs[adapter]
		t.Run(adapter, func(t *testing.T) {
			files := capabilityProbeFiles(adapter)
			snapshot, err := NewSnapshot(1, files, "capability-basis-"+adapter)
			if err != nil {
				t.Fatal(err)
			}
			registry.RegisterConfig(adapter, cfg)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			measurement, err := registry.RefreshCapability(ctx, adapter, CanonicalCapabilityConformanceProbe(snapshot, adapter))
			if err != nil {
				t.Fatalf("measuring %s capability: %v", adapter, err)
			}
			if measurement.Status != "measured" || measurement.MeasurementID == "" {
				t.Fatalf("unmeasured %s capability: %+v", adapter, measurement)
			}
		})
	}
	defer registry.Close()
	raw, err := registry.CapabilityMatrixJSON()
	if err != nil {
		t.Fatalf("serialize capability matrix: %v", err)
	}
	if err := contractharness.Validate(evidence.CapabilityMatrixSchemaID, raw); err != nil {
		t.Fatalf("capability matrix schema validation: %v\n%s", err, raw)
	}
	for _, adapter := range []string{"dart", "typescript", "go"} {
		measurement := registry.CapabilityMatrix().Measurement(adapter)
		if measurement.Status != "measured" || measurement.MeasurementID == "" || measurement.AdapterVersion == "" || measurement.AnalyzerRevision == "" {
			t.Fatalf("capability matrix entry %s is not measured from initialize evidence: %+v", adapter, measurement)
		}
	}
}

func capabilityProbeFiles(adapter string) map[string]string {
	switch adapter {
	case "dart":
		return map[string]string{
			"pubspec.yaml":  "name: capability_probe\nenvironment:\n  sdk: '>=3.0.0 <4.0.0'\n",
			"lib/main.dart": "void main() {}\n",
		}
	case "typescript":
		return map[string]string{
			"package.json": "{\"name\":\"capability-probe\"}\n",
			"src/main.ts":  "export function probe(): void {}\n",
		}
	default:
		return map[string]string{
			"go.mod":  "module capability.probe\n\ngo 1.22\n",
			"main.go": "package main\nfunc main() {}\n",
		}
	}
}
