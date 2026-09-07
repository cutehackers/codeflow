package protocol

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/rflscvs02"
)

func TestCapabilityRegistryPublishesSchemaValidatedMeasuredMatrix(t *testing.T) {
	adapterRegistry, measuredAt := refreshMeasuredGoCapability(t, time.Hour)
	defer adapterRegistry.Close()
	registry := adapterRegistry.CapabilityRegistry()
	measurement := registry.SnapshotAt(measuredAt).Measurement("go")
	if measurement.Status != "measured" || !measurement.Supports("read_only_source") {
		t.Fatalf("published measurement = %+v", measurement)
	}
	raw, err := registry.MatrixJSONAt(measuredAt)
	if err != nil {
		t.Fatalf("MatrixJSONAt() error = %v", err)
	}
	if err := contractharness.Validate(rflscvs02.CapabilityMatrixSchemaID, raw); err != nil {
		t.Fatalf("published matrix is not schema-valid: %v\n%s", err, raw)
	}
	if got := registry.SnapshotAt(measuredAt).Measurement("go"); got.Status != "measured" {
		t.Fatalf("snapshot lost measured Go capability: %+v", got)
	}
}

func TestCapabilityRegistryKeepsUnprobedAdaptersUnsupported(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	registry := NewCapabilityRegistry(time.Hour)
	snapshot := registry.SnapshotAt(now)
	for _, adapter := range []string{"dart", "typescript", "go"} {
		measurement := snapshot.Measurement(adapter)
		if measurement.Status != "unsupported" || len(measurement.Features) != 0 {
			t.Fatalf("unprobed %s capability was promoted: %+v", adapter, measurement)
		}
		if measurement.MeasurementID == "" || !containsCapabilityString(measurement.Evidence, "missing_or_unprobed") {
			t.Fatalf("unprobed %s capability lacks provenance: %+v", adapter, measurement)
		}
	}
	if raw, err := registry.MatrixJSONAt(now); err != nil {
		t.Fatalf("unprobed matrix should still serialize: %v", err)
	} else if err := contractharness.Validate(rflscvs02.CapabilityMatrixSchemaID, raw); err != nil {
		t.Fatalf("unprobed matrix is not schema-valid: %v\n%s", err, raw)
	}
}

func TestCapabilityRegistryRejectsForgedMeasuredPublication(t *testing.T) {
	registry := NewCapabilityRegistry(time.Hour)
	err := registry.publishMeasurement(rflscvs02.CapabilityMeasurement{
		Adapter: "go", AdapterVersion: "go/1", ProtocolVersion: 1,
		AnalyzerRevision: "go-analyzer/1", MeasurementID: "forged",
		Status: "measured", Features: []string{"read_only_source"}, Unsupported: []string{}, Evidence: []string{
			"initialize", "protocol_conformance", "probe:forged", "observation:snapshot_bytes", "observation:read_set",
			"observation:membership", "observation:negative_lookup", "observation:dependency_frontier", "open_on_missing", "read_only_source",
		},
	}, time.Now().UTC(), CapabilityConformanceEvidence{
		ProbeID: "forged", MeasuredObservations: append([]string(nil), canonicalCapabilityObservations...), OpenOnMissing: true, ReadOnlySource: true,
	})
	if err == nil {
		t.Fatal("forged measured capability was published without canonical probe provenance")
	}
	if got := registry.Snapshot().Measurement("go"); got.Status != "unsupported" {
		t.Fatalf("forged publication changed capability state: %+v", got)
	}
}

func TestAdapterRegistryRefreshPublishesAndCachesCapability(t *testing.T) {
	registry := NewAdapterRegistry(1)
	root := repoRootDir(t)
	goAdapter := filepath.Join(t.TempDir(), "codeflow-go-adapter")
	build := exec.Command("go", "build", "-o", goAdapter, "./adapters/go")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Go adapter: %v\n%s", err, output)
	}
	registry.RegisterConfig("go", Config{BinPath: goAdapter, DefaultTimeout: 2 * time.Second})
	snapshot, err := NewSnapshot(1, map[string]string{
		"go.mod":  "module capability.probe\n\ngo 1.22\n",
		"main.go": "package main\nfunc main() {}\n",
	}, "capability-registry-basis")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	probeCalls := 0
	canonical := CanonicalCapabilityConformanceProbe(snapshot, "go")
	measurement, err := registry.RefreshCapability(ctx, "go", func(probeCtx context.Context, conn *Conn) (CapabilityConformanceEvidence, error) {
		probeCalls++
		if conn.Version().AdapterVersion == "" || conn.Version().AnalyzerVersion == "" {
			t.Fatal("probe did not observe initialize evidence")
		}
		return canonical(probeCtx, conn)
	})
	if err != nil {
		t.Fatalf("RefreshCapability() error = %v", err)
	}
	if measurement.Status != "measured" || probeCalls != 1 {
		t.Fatalf("refresh result = %+v, probeCalls=%d", measurement, probeCalls)
	}
	if got := registry.CapabilityMatrix().Measurement("go"); got.Status != "measured" {
		t.Fatalf("product registry did not publish measured capability: %+v", got)
	}
	if probeCalls != 1 {
		t.Fatal("ordinary capability retrieval reran the executable conformance probe")
	}
}

func TestAdapterRegistryRefreshRejectsUnvalidatedConformanceReport(t *testing.T) {
	registry := NewAdapterRegistry(1)
	registry.RegisterConfig("go", Config{BinPath: buildMockAdapter(t), DefaultTimeout: 2 * time.Second})
	defer registry.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	measurement, err := registry.RefreshCapability(ctx, "go", func(_ context.Context, _ *Conn) (CapabilityConformanceEvidence, error) {
		return CapabilityConformanceEvidence{
			ProbeID: "forged/probe", MeasuredObservations: append([]string(nil), canonicalCapabilityObservations...),
			OpenOnMissing: true, ReadOnlySource: true,
			// validated is intentionally false. A report with no canonical
			// executable proof must remain unsupported.
		}, nil
	})
	if err == nil || measurement.Status != "unsupported" {
		t.Fatalf("unvalidated conformance report was promoted: measurement=%+v err=%v", measurement, err)
	}
	if got := registry.CapabilityMatrix().Measurement("go"); got.Status != "unsupported" {
		t.Fatalf("unvalidated refresh changed published state: %+v", got)
	}
}

func TestCapabilityRegistryExpiresCachedMeasurementWithoutImplicitProbe(t *testing.T) {
	adapterRegistry, measuredAt := refreshMeasuredGoCapability(t, time.Hour)
	defer adapterRegistry.Close()
	registry := adapterRegistry.CapabilityRegistry()
	snapshot := registry.SnapshotAt(measuredAt.Add(time.Hour + time.Nanosecond))
	measurement := snapshot.Measurement("go")
	if measurement.Status != "unsupported" || len(measurement.Features) != 0 {
		t.Fatalf("expired measurement was promoted: %+v", measurement)
	}
	if !containsCapabilityString(measurement.Evidence, "expired_measurement") {
		t.Fatalf("expired measurement lacks bounded lifetime evidence: %+v", measurement)
	}
	provenance := snapshot.Provenance["go"]
	if provenance.Status != "unsupported" || provenance.ExpiresAt == "" {
		t.Fatalf("expired provenance = %+v", provenance)
	}
	if raw, err := registry.MatrixJSONAt(measuredAt.Add(2 * time.Hour)); err != nil {
		t.Fatalf("expired matrix should serialize: %v", err)
	} else if err := contractharness.Validate(rflscvs02.CapabilityMatrixSchemaID, raw); err != nil {
		t.Fatalf("expired matrix is not schema-valid: %v\n%s", err, raw)
	}
}

func refreshMeasuredGoCapability(t *testing.T, ttl time.Duration) (*AdapterRegistry, time.Time) {
	t.Helper()
	root := repoRootDir(t)
	goAdapter := filepath.Join(t.TempDir(), "codeflow-go-adapter")
	build := exec.Command("go", "build", "-o", goAdapter, "./adapters/go")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Go adapter: %v\n%s", err, output)
	}
	registry := NewAdapterRegistry(1)
	registry.capabilityRegistry = NewCapabilityRegistry(ttl)
	registry.RegisterConfig("go", Config{BinPath: goAdapter, DefaultTimeout: 2 * time.Second})
	snapshot, err := NewSnapshot(1, map[string]string{
		"go.mod":  "module capability.probe\n\ngo 1.22\n",
		"main.go": "package main\nfunc main() {}\n",
	}, "capability-registry-basis")
	if err != nil {
		registry.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := registry.RefreshCapability(ctx, "go", CanonicalCapabilityConformanceProbe(snapshot, "go")); err != nil {
		registry.Close()
		t.Fatalf("refresh Go capability: %v", err)
	}
	return registry, time.Now().UTC()
}

func containsCapabilityString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
