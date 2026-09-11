package contractharness_test

import (
	"encoding/json"
	"testing"
	"time"

	harness "codeflow/internal/contractharness"
	"codeflow/internal/isolation"
)

func TestVS06RuntimeConsentValidation(t *testing.T) {
	valid := readVS06Fixture(t, "rflsc.runtime-consent.v1/valid/consent.json")
	now := time.Date(2026, 9, 5, 0, 1, 0, 0, time.UTC)
	if err := harness.ValidateRuntimeConsentV1At(valid, now); err != nil {
		t.Fatalf("valid consent rejected: %v", err)
	}

	tests := []struct {
		name string
		data []byte
		now  time.Time
	}{
		{
			name: "missing actor",
			data: func() []byte {
				doc := cloneVS06Document(t, valid)
				delete(doc, "actorId")
				return marshalVS06Document(t, doc)
			}(),
			now: now,
		},
		{
			name: "false approval",
			data: readVS06Fixture(t, "rflsc.runtime-consent.v1/invalid/not-approved.json"),
			now:  now,
		},
		{
			name: "expired approval",
			data: valid,
			now:  time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := harness.ValidateRuntimeConsentV1At(tt.data, tt.now); err == nil {
				t.Fatalf("expected %s consent to be rejected", tt.name)
			}
		})
	}

	var consent isolation.RuntimeConsentV1
	if err := json.Unmarshal(valid, &consent); err != nil {
		t.Fatalf("decode valid consent: %v", err)
	}
	spec := isolation.RuntimeExecutionSpec{
		Command:       isolation.RuntimeCommand{Command: consent.Command, Args: append([]string(nil), consent.Args...)},
		CommandDigest: consent.CommandDigest,
		AccessScope:   consent.AccessScope, IsolationScope: consent.IsolationScope,
	}
	if err := consent.Matches(spec, consent.SnapshotID, consent.SnapshotTreeDigest, consent.Nonce); err != nil {
		t.Fatalf("matching consent rejected: %v", err)
	}
	for _, tt := range []struct {
		name   string
		mutate func(*isolation.RuntimeExecutionSpec, *isolation.RuntimeConsentV1, *string)
	}{
		{
			name: "command mismatch",
			mutate: func(spec *isolation.RuntimeExecutionSpec, _ *isolation.RuntimeConsentV1, _ *string) {
				spec.Command.Command = "/bin/sh"
			},
		},
		{
			name: "access scope mismatch",
			mutate: func(spec *isolation.RuntimeExecutionSpec, _ *isolation.RuntimeConsentV1, _ *string) {
				spec.AccessScope.Network = "loopback"
			},
		},
		{
			name: "snapshot mismatch",
			mutate: func(_ *isolation.RuntimeExecutionSpec, consent *isolation.RuntimeConsentV1, _ *string) {
				consent.SnapshotID = "different-snapshot"
			},
		},
		{
			name: "replayed nonce",
			mutate: func(_ *isolation.RuntimeExecutionSpec, _ *isolation.RuntimeConsentV1, nonce *string) {
				*nonce = "nonce-vs06-replayed"
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			candidateSpec := spec
			candidateSpec.Command.Args = append([]string(nil), spec.Command.Args...)
			candidateConsent := consent
			candidateConsent.Args = append([]string(nil), consent.Args...)
			nonce := consent.Nonce
			tt.mutate(&candidateSpec, &candidateConsent, &nonce)
			if err := candidateConsent.Matches(candidateSpec, consent.SnapshotID, consent.SnapshotTreeDigest, nonce); err == nil {
				t.Fatalf("expected %s consent binding to be rejected", tt.name)
			}
		})
	}
}

func TestVS06RuntimeIsolationResultValidation(t *testing.T) {
	valid := readVS06Fixture(t, "rflsc.runtime-isolation-result.v1/valid/success.json")
	if err := harness.ValidateRuntimeIsolationResultV1(valid); err != nil {
		t.Fatalf("valid isolation result rejected: %v", err)
	}

	for _, status := range []string{
		isolation.RuntimeTerminalSuccess,
		isolation.RuntimeTerminalFailure,
		isolation.RuntimeTerminalTimeout,
		isolation.RuntimeTerminalCancel,
	} {
		t.Run(status+" cleanup", func(t *testing.T) {
			doc := cloneVS06Document(t, valid)
			doc["status"] = status
			if status != isolation.RuntimeTerminalSuccess {
				doc["resultCode"] = status
				doc["evidencePromotion"] = isolation.RuntimePromotionBlocked
				doc["promotionBlockedReason"] = "terminal outcome is not promotable"
			}
			if err := harness.ValidateRuntimeIsolationResultV1(marshalVS06Document(t, doc)); err != nil {
				t.Fatalf("%s result with verified cleanup rejected: %v", status, err)
			}
		})
	}

	for _, tt := range []struct {
		name string
		data []byte
	}{
		{name: "tree digest mismatch", data: readVS06Fixture(t, "rflsc.runtime-isolation-result.v1/invalid/tree-mismatch.json")},
		{name: "caller supplied result", data: readVS06Fixture(t, "rflsc.runtime-isolation-result.v1/invalid/caller-result.json")},
		{name: "unavailable audit promoted", data: readVS06Fixture(t, "rflsc.runtime-isolation-result.v1/invalid/audit-unavailable-promoted.json")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := harness.ValidateRuntimeIsolationResultV1(tt.data); err == nil {
				t.Fatalf("expected %s to be rejected", tt.name)
			}
		})
	}

	t.Run("recomputed digest mismatch blocks promotion", func(t *testing.T) {
		doc := cloneVS06Document(t, valid)
		doc["recomputedTreeDigest"] = "tree-vs06-tampered"
		if err := harness.ValidateRuntimeIsolationResultV1(marshalVS06Document(t, doc)); err == nil {
			t.Fatal("expected a recomputed input tree digest mismatch to be rejected")
		}
	})

	t.Run("runtime attributed source write blocks promotion", func(t *testing.T) {
		doc := cloneVS06Document(t, valid)
		doc["resultCode"] = isolation.RuntimeAuditViolation
		doc["sourceWriteAuditStatus"] = isolation.RuntimeAuditViolation
		doc["sourceIntegrityStatus"] = isolation.RuntimeAuditViolation
		doc["evidencePromotion"] = isolation.RuntimePromotionBlocked
		doc["promotionBlockedReason"] = "runtime-attributed repository write"
		doc["repositoryPathWriteAudit"] = map[string]any{
			"codeflowWriteCount": 1, "repositoryPathWrites": []any{"internal/generated.go"},
			"sourceIntegrityViolation": true, "capturedSnapshotTreeDigest": "tree-vs06",
		}
		if err := harness.ValidateRuntimeIsolationResultV1(marshalVS06Document(t, doc)); err != nil {
			t.Fatalf("source write result rejected before promotion gate: %v", err)
		}
	})

	t.Run("audit unavailable stays blocked", func(t *testing.T) {
		doc := cloneVS06Document(t, valid)
		doc["sourceWriteAuditStatus"] = isolation.RuntimeAuditUnavailable
		doc["sourceIntegrityStatus"] = "audit_unavailable"
		doc["evidencePromotion"] = isolation.RuntimePromotionBlocked
		doc["promotionBlockedReason"] = "source write telemetry unavailable"
		doc["resultCode"] = isolation.RuntimeAuditUnavailable
		if err := harness.ValidateRuntimeIsolationResultV1(marshalVS06Document(t, doc)); err != nil {
			t.Fatalf("blocked unavailable-audit result rejected: %v", err)
		}
	})

	t.Run("concurrent edit is reconciled separately", func(t *testing.T) {
		doc := cloneVS06Document(t, valid)
		doc["concurrentWorktree"] = map[string]any{
			"classification":   "reconciled_unattributed",
			"beforeSnapshotId": "snapshot-vs06",
			"afterSnapshotId":  "snapshot-vs06-live-edit",
			"beforeTreeDigest": "tree-vs06",
			"afterTreeDigest":  "tree-vs06-live-edit",
		}
		if err := harness.ValidateRuntimeIsolationResultV1(marshalVS06Document(t, doc)); err != nil {
			t.Fatalf("concurrent edit was attributed to runtime: %v", err)
		}
	})

	t.Run("cleanup is mandatory", func(t *testing.T) {
		doc := cloneVS06Document(t, valid)
		doc["cleanup"] = map[string]any{"layerCreated": true, "layerDisposed": false, "verified": false}
		if err := harness.ValidateRuntimeIsolationResultV1(marshalVS06Document(t, doc)); err == nil {
			t.Fatal("expected missing disposable-layer cleanup to block validation")
		}
	})
}
