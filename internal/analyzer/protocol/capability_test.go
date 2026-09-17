package protocol

import (
	"context"
	"testing"
	"time"
)

func TestPoolMeasureCapabilityUsesInitializeAndExecutableProbe(t *testing.T) {
	bin := buildMockAdapter(t)
	p := NewPool(Config{BinPath: bin, DefaultTimeout: 2 * time.Second}, 1)
	defer p.Close()

	measurement, err := p.MeasureCapabilityWithConformance(context.Background(), "mock", func(_ context.Context, conn *Conn) (CapabilityConformanceEvidence, error) {
		if info := conn.Version(); info.AdapterVersion == "" || info.AnalyzerVersion == "" {
			t.Fatalf("probe did not observe negotiated initialize identity: %+v", info)
		}
		return CapabilityConformanceEvidence{
			ProbeID: "unit/mock", MeasuredObservations: append([]string(nil), canonicalCapabilityObservations...),
			OpenOnMissing: true, ReadOnlySource: true, validated: true,
		}, nil
	})
	if err != nil {
		t.Fatalf("MeasureCapability() error = %v", err)
	}
	if measurement.Status != "measured" {
		t.Fatalf("measurement status = %q, want measured: %+v", measurement.Status, measurement)
	}
	if measurement.Adapter != "mock" || measurement.AdapterVersion == "" || measurement.AnalyzerRevision == "" {
		t.Fatalf("measurement lost initialize identity: %+v", measurement)
	}
	if measurement.MeasurementID == "" || !measurement.Supports("snapshot_bytes") || !measurement.Supports("read_only_source") {
		t.Fatalf("measurement lacks measured capabilities: %+v", measurement)
	}
}

func TestPoolMeasureCapabilityLegacyProbeCannotPromoteMeasuredState(t *testing.T) {
	bin := buildMockAdapter(t)
	p := NewPool(Config{BinPath: bin, DefaultTimeout: 2 * time.Second}, 1)
	defer p.Close()

	measurement, err := p.MeasureCapability(context.Background(), "mock", func(conn *Conn) error {
		if conn.Version().AdapterVersion == "" {
			t.Fatal("legacy probe did not observe initialize evidence")
		}
		return nil
	})
	if err == nil || measurement.Status != "unsupported" {
		t.Fatalf("legacy nil-returning probe was promoted: measurement=%+v err=%v", measurement, err)
	}
}

func TestPoolMeasureCapabilityWithoutConformanceProbeIsUnsupported(t *testing.T) {
	bin := buildMockAdapter(t)
	p := NewPool(Config{BinPath: bin, DefaultTimeout: 2 * time.Second}, 1)
	defer p.Close()

	measurement, err := p.MeasureCapability(context.Background(), "mock", nil)
	if err == nil {
		t.Fatal("MeasureCapability() without executable conformance probe returned nil error")
	}
	if measurement.Status != "unsupported" || measurement.MeasurementID == "" {
		t.Fatalf("missing probe was not typed unsupported: %+v", measurement)
	}
}
