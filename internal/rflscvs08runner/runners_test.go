package rflscvs08runner

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"codeflow/internal/protocol"
	"codeflow/internal/semantic"
)

func TestRFLSCR2VS08_A01(t *testing.T) { RunA01(t) }
func TestRFLSCR2VS08_A02(t *testing.T) { RunA02(t) }
func TestRFLSCR2VS08_A03(t *testing.T) { RunA03(t) }
func TestRFLSCR2VS08_A04(t *testing.T) { RunA04(t) }
func TestRFLSCR2VS08_A05(t *testing.T) { RunA05(t) }
func TestRFLSCR2VS08_A06(t *testing.T) { RunA06(t) }
func TestRFLSCR2VS08_A07(t *testing.T) { RunA07(t) }
func TestRFLSCR2VS08_A08(t *testing.T) { RunA08(t) }
func TestRFLSCR2VS08_A09(t *testing.T) { RunA09(t) }
func TestRFLSCR2VS08_A10(t *testing.T) { RunA10(t) }

func TestRunA01DoesNotInventModelHostObservation(t *testing.T) {
	evidence := RunA01(t)
	if len(evidence.ModelHostLifecycle) != 0 {
		t.Fatalf("A1 invented model-host lifecycle observations: %+v", evidence.ModelHostLifecycle)
	}
	if !reflect.DeepEqual(evidence.MountAudit, protocol.ModelHostIsolationEvidence{}) || !reflect.DeepEqual(evidence.CapabilityAudit, protocol.ModelHostCapability{}) {
		t.Fatalf("A1 claimed measured model-host audit without an observation: mount=%+v capability=%+v", evidence.MountAudit, evidence.CapabilityAudit)
	}
}

func TestRunA03RecordsActualModelHostLifecycleModes(t *testing.T) {
	evidence := RunA03(t)
	if len(evidence.ModelHostLifecycle) != 3 {
		t.Fatalf("A3 lifecycle observations = %d, want success/timeout/cancel", len(evidence.ModelHostLifecycle))
	}
	seen := make(map[string]bool, len(evidence.ModelHostLifecycle))
	for _, observation := range evidence.ModelHostLifecycle {
		if seen[observation.Mode] {
			t.Fatalf("A3 duplicated model-host lifecycle mode %q", observation.Mode)
		}
		seen[observation.Mode] = true
		if observation.RequestID == "" || observation.Isolation.ReceivedRequestID != observation.RequestID || observation.Isolation.ReceivedPackDigest != evidence.PackDigest || !observation.Isolation.CleanupVerified {
			t.Fatalf("A3 lifecycle observation did not come from the actual host: %+v", observation)
		}
	}
	for _, mode := range []string{"success", "timeout", "cancel"} {
		if !seen[mode] {
			t.Fatalf("A3 missing actual model-host lifecycle mode %q", mode)
		}
	}
}

func TestRunA04DoesNotClaimMeasuredModelHostForUnavailable(t *testing.T) {
	evidence := RunA04(t)
	if len(evidence.ModelHostLifecycle) != 0 {
		t.Fatalf("A4 invented model-host lifecycle observations: %+v", evidence.ModelHostLifecycle)
	}
	if !reflect.DeepEqual(evidence.MountAudit, protocol.ModelHostIsolationEvidence{}) || !reflect.DeepEqual(evidence.CapabilityAudit, protocol.ModelHostCapability{}) {
		t.Fatalf("A4 claimed measured model-host audit while unavailable: mount=%+v capability=%+v", evidence.MountAudit, evidence.CapabilityAudit)
	}
}

func TestNonApplicableCriteriaDoNotEmitModelHostAudit(t *testing.T) {
	for _, test := range []struct {
		criterion string
		runner    func(*testing.T) Evidence
	}{
		{criterion: "VS08-A1", runner: RunA01},
		{criterion: "VS08-A2", runner: RunA02},
		{criterion: "VS08-A4", runner: RunA04},
		{criterion: "VS08-A5", runner: RunA05},
		{criterion: "VS08-A6", runner: RunA06},
		{criterion: "VS08-A8", runner: RunA08},
		{criterion: "VS08-A9", runner: RunA09},
	} {
		t.Run(test.criterion, func(t *testing.T) {
			evidence := test.runner(t)
			if evidence.ModelHostAuditApplicability != ModelHostAuditNotApplicable || len(evidence.ModelHostLifecycle) != 0 || !reflect.DeepEqual(evidence.MountAudit, protocol.ModelHostIsolationEvidence{}) || !reflect.DeepEqual(evidence.CapabilityAudit, protocol.ModelHostCapability{}) {
				t.Fatalf("%s emitted model-host audit despite being non-applicable: %+v", test.criterion, evidence)
			}
		})
	}
}

func TestRunA07UsesSemanticTimeoutObservation(t *testing.T) {
	evidence := RunA07(t)
	if len(evidence.ModelHostLifecycle) != 1 || evidence.ModelHostLifecycle[0].Mode != "timeout" {
		t.Fatalf("A7 did not record its semantic timeout host observation: %+v", evidence.ModelHostLifecycle)
	}
	observation := evidence.ModelHostLifecycle[0]
	if observation.RequestID == "" || observation.Isolation.ReceivedRequestID != observation.RequestID || observation.Isolation.ReceivedPackDigest != evidence.PackDigest || observation.Isolation.TerminalStatus != "timeout" || !observation.Isolation.CleanupVerified {
		t.Fatalf("A7 timeout evidence does not match the actual execution: %+v", observation)
	}
}

func TestValidateEvidenceRejectsModelHostApplicabilityContradictions(t *testing.T) {
	tests := []struct {
		name   string
		base   func(*testing.T) Evidence
		mutate func(*Evidence)
	}{
		{name: "non-applicable criterion cannot claim observed host", base: RunA01, mutate: func(value *Evidence) {
			value.ModelHostAuditApplicability = ModelHostAuditApplicable
			value.ModelHostOutcome = "observed"
		}},
		{name: "unavailable criterion cannot claim observed host", base: RunA04, mutate: func(value *Evidence) {
			value.ModelHostOutcome = "observed"
		}},
		{name: "applicable criterion cannot omit host observation", base: RunA03, mutate: func(value *Evidence) {
			value.ModelHostAuditApplicability = ModelHostAuditNotApplicable
			value.ModelHostOutcome = "not_applicable"
			value.ModelHostLifecycle = nil
			value.MountAudit = protocol.ModelHostIsolationEvidence{}
			value.CapabilityAudit = protocol.ModelHostCapability{}
		}},
		{name: "A3 requires every declared mode", base: RunA03, mutate: func(value *Evidence) {
			value.ModelHostLifecycle = append([]ModelHostObservation(nil), value.ModelHostLifecycle[:2]...)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := test.base(t)
			test.mutate(&mutated)
			if err := ValidateEvidence(mutated); err == nil {
				t.Fatalf("applicability contradiction was accepted: %+v", mutated)
			}
		})
	}
}

func TestValidateEvidenceRequiresExactArtifactRefsForEveryApplicability(t *testing.T) {
	bases := []Evidence{RunA01(t), RunA03(t)}
	mutations := []struct {
		name   string
		mutate func(*Evidence)
	}{
		{name: "empty", mutate: func(value *Evidence) { value.ObjectRefs[0] = "" }},
		{name: "duplicate", mutate: func(value *Evidence) { value.ObjectRefs[4] = value.ObjectRefs[0] }},
		{name: "wrong prefix", mutate: func(value *Evidence) { value.ObjectRefs[0] = "tree:" + value.SnapshotID }},
		{name: "wrong value", mutate: func(value *Evidence) { value.ObjectRefs[0] = "snapshot:other" }},
		{name: "other identity alias", mutate: func(value *Evidence) { value.ObjectRefs[0] = "snapshot:" + value.ComputedBasisID }},
		{name: "extra", mutate: func(value *Evidence) { value.ObjectRefs = append(value.ObjectRefs, "extra:unexpected") }},
		{name: "missing", mutate: func(value *Evidence) { value.ObjectRefs = value.ObjectRefs[:4] }},
	}
	for _, base := range bases {
		for _, mutation := range mutations {
			t.Run(base.Criterion+"/"+mutation.name, func(t *testing.T) {
				mutated := cloneRunnerEvidence(base)
				mutation.mutate(&mutated)
				if err := ValidateEvidence(mutated); err == nil {
					t.Fatalf("%s accepted non-canonical artifact refs: %+v", mutation.name, mutated.ObjectRefs)
				}
			})
		}
	}
}

func TestRunA10_UsesObservedModelHostLifecycleEvidence(t *testing.T) {
	evidence := RunA10(t)
	if evidence.ModelHostAuditApplicability != ModelHostAuditApplicable || evidence.ModelHostOutcome != "observed" {
		t.Fatalf("A10 model-host applicability/outcome = %q/%q", evidence.ModelHostAuditApplicability, evidence.ModelHostOutcome)
	}
	if evidence.ConcurrentPackAudit == nil || !evidence.ConcurrentPackAudit.Verified() || evidence.ConcurrentPackEditVerified != evidence.ConcurrentPackAudit.Verified() {
		t.Fatalf("A10 concurrent pack audit is missing or boolean-only: audit=%+v verified=%v", evidence.ConcurrentPackAudit, evidence.ConcurrentPackEditVerified)
	}
	if evidence.ConcurrentPackAudit.First.HostIdentity == evidence.ConcurrentPackAudit.Later.HostIdentity || evidence.ConcurrentPackAudit.First.RequestID == evidence.ConcurrentPackAudit.Later.RequestID {
		t.Fatalf("A10 concurrent requests did not use distinct Core identities: %+v", evidence.ConcurrentPackAudit)
	}
	if len(evidence.ModelHostLifecycle) != 5 {
		t.Fatalf("model host lifecycle observations = %d, want success/failure/crash/timeout/cancel", len(evidence.ModelHostLifecycle))
	}
	seen := make(map[string]bool, len(evidence.ModelHostLifecycle))
	for _, observation := range evidence.ModelHostLifecycle {
		if observation.Applicability != ModelHostAuditApplicable {
			t.Fatalf("unscoped A10 model-host observation: %+v", observation)
		}
		seen[observation.Mode] = true
		if observation.RequestID == "" || observation.Isolation.PackDigest != evidence.PackDigest || observation.Isolation.ReceivedRequestID != observation.RequestID || observation.Isolation.ReceivedPackDigest != evidence.PackDigest || observation.Isolation.SourceMount != "not_mounted" || observation.Isolation.RepositoryPathExposed || observation.Isolation.RepositoryWriteCapability || len(observation.Isolation.RepositoryWriteAttempts) != 0 || observation.Isolation.RepositoryWriteAuditStatus != protocol.ModelHostRepositoryWriteAuditCapabilityEnforced || observation.Isolation.DisposableWriteAttempt != "blocked" || !observation.Isolation.CoreTrustedProbe || observation.Isolation.WorkingDirectoryPermission != "0700" || !observation.Isolation.Disposable || !observation.Isolation.CleanupVerified || observation.Isolation.RuntimePolicyBinding != protocol.ModelHostRuntimePolicyBindingExact || observation.Isolation.ProbeScope != protocol.ModelHostProbeScopeSharedDenyBase || observation.Isolation.RuntimePolicySharedBaseDigest == "" || observation.Isolation.ProbePolicyDigest == "" || observation.Isolation.ProbeSharedBaseDigest == "" || observation.Isolation.ProbeSharedBaseDigest != observation.Isolation.RuntimePolicySharedBaseDigest {
			t.Fatalf("incomplete observed model host lifecycle: %+v", observation)
		}
		if observation.Capability.Status != "measured" || !observation.Capability.Measured {
			t.Fatalf("unmeasured observed capability: %+v", observation.Capability)
		}
	}
	for _, mode := range []string{"success", "failure", "crash", "timeout", "cancel"} {
		if !seen[mode] {
			t.Fatalf("missing observed model host mode %q", mode)
		}
	}
	if !evidence.ConcurrentPackEditVerified {
		t.Fatal("concurrent live edit was not proven to affect only a later pack")
	}
}

func TestEvidenceDeepCopiesNestedModelHostEvidence(t *testing.T) {
	f := newFixture(t)
	pack := buildPack(t, f)
	isoLimits := &protocol.ModelHostResourceLimitEvidence{Version: protocol.ModelHostResourceLimitsVersion}
	capLimits := &protocol.ModelHostResourceLimitEvidence{Version: protocol.ModelHostResourceLimitsVersion}
	observation := ModelHostObservation{
		Applicability: ModelHostAuditApplicable,
		Mode:          "success",
		RequestID:     "request-copy",
		Isolation: protocol.ModelHostIsolationEvidence{
			RepositoryWriteAttempts: []string{"attempt-source"},
			ResourceLimits:          isoLimits,
		},
		Capability: protocol.ModelHostCapability{
			Capabilities:   []string{"cap-source"},
			IsolationProbe: &protocol.ModelHostIsolationProbe{RepositoryReadAttempt: "blocked"},
			ResourceLimits: capLimits,
		},
	}
	source := []ModelHostObservation{observation}
	got := evidence(t, "VS08-A3", f, pack, source...)
	if got.MountAudit.ResourceLimits == got.ModelHostLifecycle[0].Isolation.ResourceLimits || got.CapabilityAudit.ResourceLimits == got.ModelHostLifecycle[0].Capability.ResourceLimits {
		t.Fatal("evidence top-level audit shares resource-limit pointers with lifecycle observation")
	}

	source[0].Isolation.RepositoryWriteAttempts[0] = "source-mutated"
	source[0].Isolation.ResourceLimits.Version = 99
	source[0].Capability.Capabilities[0] = "source-mutated"
	source[0].Capability.IsolationProbe.RepositoryReadAttempt = "source-mutated"
	source[0].Capability.ResourceLimits.Version = 99
	if got.ModelHostLifecycle[0].Isolation.RepositoryWriteAttempts[0] != "attempt-source" || got.ModelHostLifecycle[0].Isolation.ResourceLimits.Version != protocol.ModelHostResourceLimitsVersion || got.ModelHostLifecycle[0].Capability.Capabilities[0] != "cap-source" || got.ModelHostLifecycle[0].Capability.IsolationProbe.RepositoryReadAttempt != "blocked" || got.ModelHostLifecycle[0].Capability.ResourceLimits.Version != protocol.ModelHostResourceLimitsVersion {
		t.Fatal("source observation mutation changed evidence output")
	}

	got.ModelHostLifecycle[0].Isolation.RepositoryWriteAttempts[0] = "lifecycle-mutated"
	got.ModelHostLifecycle[0].Isolation.ResourceLimits.Version = 98
	got.ModelHostLifecycle[0].Capability.Capabilities[0] = "lifecycle-mutated"
	got.ModelHostLifecycle[0].Capability.IsolationProbe.RepositoryReadAttempt = "lifecycle-mutated"
	got.ModelHostLifecycle[0].Capability.ResourceLimits.Version = 98
	if source[0].Isolation.RepositoryWriteAttempts[0] != "source-mutated" || source[0].Isolation.ResourceLimits.Version != 99 || source[0].Capability.Capabilities[0] != "source-mutated" || source[0].Capability.IsolationProbe.RepositoryReadAttempt != "source-mutated" || source[0].Capability.ResourceLimits.Version != 99 {
		t.Fatal("evidence lifecycle mutation changed source observation")
	}

	got.MountAudit.RepositoryWriteAttempts[0] = "mount-mutated"
	got.CapabilityAudit.Capabilities[0] = "capability-mutated"
	if got.ModelHostLifecycle[0].Isolation.RepositoryWriteAttempts[0] != "lifecycle-mutated" || got.ModelHostLifecycle[0].Capability.Capabilities[0] != "lifecycle-mutated" {
		t.Fatal("evidence top-level audit mutation changed lifecycle observation")
	}
	got.RepositoryWriteAudit[0] = "audit-mutated"
	if got.MountAudit.RepositoryWriteAttempts[0] != "mount-mutated" {
		t.Fatal("evidence repository audit slice aliases mount audit")
	}
	second := evidence(t, "VS08-A3", f, pack, source...)
	got.ObjectRefs[0] = "object-ref-mutated"
	if second.ObjectRefs[0] == "object-ref-mutated" {
		t.Fatal("evidence object references share backing storage across conversions")
	}
}

func TestConcurrentPackAuditCloneDeepCopiesNestedEvidencePreservesBinding(t *testing.T) {
	base := RunA10(t)
	if base.ConcurrentPackAudit == nil {
		t.Fatal("A10 did not produce a concurrent-pack audit")
	}
	audit := *base.ConcurrentPackAudit
	audit.First.Isolation.RepositoryWriteAttempts = []string{"first-attempt"}
	audit.First.Isolation.ResourceLimits = &protocol.ModelHostResourceLimitEvidence{Version: 1, Backend: "first"}
	audit.First.Capability.Capabilities = []string{"first-capability"}
	audit.First.Capability.IsolationProbe = &protocol.ModelHostIsolationProbe{RepositoryReadAttempt: "first"}
	audit.First.Capability.ResourceLimits = &protocol.ModelHostResourceLimitEvidence{Version: 1, Backend: "first-capability"}
	audit.Later.Isolation.RepositoryWriteAttempts = []string{"later-attempt"}
	audit.Later.Isolation.ResourceLimits = &protocol.ModelHostResourceLimitEvidence{Version: 1, Backend: "later"}
	audit.Later.Capability.Capabilities = []string{"later-capability"}
	audit.Later.Capability.IsolationProbe = &protocol.ModelHostIsolationProbe{RepositoryReadAttempt: "later"}
	audit.Later.Capability.ResourceLimits = &protocol.ModelHostResourceLimitEvidence{Version: 1, Backend: "later-capability"}
	clone := cloneRunnerConcurrentPackAudit(&audit)
	if clone == nil || clone.First.host != audit.First.host || clone.First.attestation != audit.First.attestation || clone.Later.host != audit.Later.host || clone.Later.attestation != audit.Later.attestation {
		t.Fatal("concurrent-pack audit clone did not preserve exact private host bindings")
	}
	if clone.First.Isolation.ResourceLimits == audit.First.Isolation.ResourceLimits || clone.First.Capability.ResourceLimits == audit.First.Capability.ResourceLimits || clone.First.Capability.IsolationProbe == audit.First.Capability.IsolationProbe || clone.Later.Isolation.ResourceLimits == audit.Later.Isolation.ResourceLimits || clone.Later.Capability.ResourceLimits == audit.Later.Capability.ResourceLimits || clone.Later.Capability.IsolationProbe == audit.Later.Capability.IsolationProbe {
		t.Fatal("concurrent-pack audit clone shares nested pointers with its source")
	}
	clone.First.Isolation.RepositoryWriteAttempts[0] = "clone-mutated"
	clone.First.Isolation.ResourceLimits.Backend = "clone-mutated"
	clone.First.Capability.Capabilities[0] = "clone-mutated"
	clone.First.Capability.IsolationProbe.RepositoryReadAttempt = "clone-mutated"
	clone.First.Capability.ResourceLimits.Backend = "clone-mutated"
	clone.Later.Isolation.RepositoryWriteAttempts[0] = "clone-mutated"
	clone.Later.Isolation.ResourceLimits.Backend = "clone-mutated"
	clone.Later.Capability.Capabilities[0] = "clone-mutated"
	clone.Later.Capability.IsolationProbe.RepositoryReadAttempt = "clone-mutated"
	clone.Later.Capability.ResourceLimits.Backend = "clone-mutated"
	if audit.First.Isolation.RepositoryWriteAttempts[0] != "first-attempt" || audit.First.Isolation.ResourceLimits.Backend != "first" || audit.First.Capability.Capabilities[0] != "first-capability" || audit.First.Capability.IsolationProbe.RepositoryReadAttempt != "first" || audit.First.Capability.ResourceLimits.Backend != "first-capability" || audit.Later.Isolation.RepositoryWriteAttempts[0] != "later-attempt" || audit.Later.Isolation.ResourceLimits.Backend != "later" || audit.Later.Capability.Capabilities[0] != "later-capability" || audit.Later.Capability.IsolationProbe.RepositoryReadAttempt != "later" || audit.Later.Capability.ResourceLimits.Backend != "later-capability" {
		t.Fatal("concurrent-pack audit clone mutation changed its source")
	}
	if err := ValidateEvidence(base); err != nil {
		t.Fatalf("source A10 evidence lost validation after clone mutation: %v", err)
	}
}

func TestWaitForConcurrentPackObservationAllowsBoundedDelay(t *testing.T) {
	f := newFixture(t)
	pack := buildPack(t, f)
	host, err := protocol.SpawnModelHost(context.Background(), protocol.ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestRFLSCR2VS08ModelHostHelper"},
		Env: []string{
			"CODEFLOW_VS08_MODEL_HOST_HELPER=1",
			"CODEFLOW_VS08_MODEL_HOST_MODE=record",
			"CODEFLOW_VS08_MODEL_HOST_RECORD=packs.log",
			"CODEFLOW_VS08_MODEL_HOST_DELAY=before-receipt:1100ms",
		},
		DisposableRoot: t.TempDir(), DefaultTimeout: modelHostAcceptanceDefaultTimeout,
	})
	if err != nil {
		t.Fatalf("spawn delayed concurrent-edit model host: %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			if cleanupErr := host.Close(); cleanupErr != nil {
				t.Errorf("cleanup delayed concurrent-edit model host: %v", cleanupErr)
			}
		}
	})
	request, err := protocol.NewModelHostRequest("request-vs08-delayed-observation", "step-submit", "Submit", "prompt-vs08", pack.PackDigest, pack, 1<<20)
	if err != nil {
		t.Fatalf("build delayed concurrent-edit request: %v", err)
	}
	result := make(chan error, 1)
	go func() {
		_, enrichErr := host.Enrich(context.Background(), request)
		result <- enrichErr
	}()
	started := time.Now()
	data := waitForConcurrentPackObservation(t, host, request, false)
	if elapsed := time.Since(started); elapsed <= time.Second {
		t.Fatalf("delayed observation did not exercise the former one-second deadline: %s", elapsed)
	}
	if len(data) == 0 {
		t.Fatal("delayed concurrent-edit observation was empty")
	}
	if enrichErr := <-result; enrichErr != nil {
		t.Fatalf("delayed concurrent-edit enrichment: %v", enrichErr)
	}
	if closeErr := host.Close(); closeErr != nil {
		t.Fatalf("close delayed concurrent-edit model host: %v", closeErr)
	}
	closed = true
}

func TestValidateA10RejectsBooleanOnlyConcurrentPackEvidence(t *testing.T) {
	base := RunA10(t)
	base.ConcurrentPackAudit = nil
	if err := ValidateEvidence(base); err == nil {
		t.Fatal("A10 accepted caller-set concurrent-pack bool without structured audit")
	}
}

func cloneConcurrentPackExecution(value ConcurrentPackExecutionEvidence) ConcurrentPackExecutionEvidence {
	clone := value
	clone.Isolation.RepositoryWriteAttempts = append([]string(nil), value.Isolation.RepositoryWriteAttempts...)
	clone.Isolation.ResourceLimits = cloneResourceEvidence(value.Isolation.ResourceLimits)
	clone.Capability.Capabilities = append([]string(nil), value.Capability.Capabilities...)
	if value.Capability.IsolationProbe != nil {
		probe := *value.Capability.IsolationProbe
		clone.Capability.IsolationProbe = &probe
	}
	clone.Capability.ResourceLimits = cloneResourceEvidence(value.Capability.ResourceLimits)
	return clone
}

func cloneConcurrentPackAudit(value *ConcurrentPackEditAudit) *ConcurrentPackEditAudit {
	if value == nil {
		return nil
	}
	clone := *value
	clone.First = cloneConcurrentPackExecution(value.First)
	clone.Later = cloneConcurrentPackExecution(value.Later)
	return &clone
}

func TestValidateA10RejectsConcurrentPackAuditMutation(t *testing.T) {
	base := RunA10(t)
	tests := []struct {
		name   string
		mutate func(*Evidence)
	}{
		{name: "missing audit", mutate: func(value *Evidence) { value.ConcurrentPackAudit = nil }},
		{name: "missing first execution", mutate: func(value *Evidence) { value.ConcurrentPackAudit.First = ConcurrentPackExecutionEvidence{} }},
		{name: "missing later execution", mutate: func(value *Evidence) { value.ConcurrentPackAudit.Later = ConcurrentPackExecutionEvidence{} }},
		{name: "reused host identity", mutate: func(value *Evidence) {
			value.ConcurrentPackAudit.Later.HostIdentity = value.ConcurrentPackAudit.First.HostIdentity
		}},
		{name: "arbitrary distinct host identity", mutate: func(value *Evidence) {
			value.ConcurrentPackAudit.Later.HostIdentity = "model-host-ffffffffffffffff"
		}},
		{name: "missing exact attestation", mutate: func(value *Evidence) {
			value.ConcurrentPackAudit.First.attestation = nil
		}},
		{name: "cross-replayed attestation", mutate: func(value *Evidence) {
			value.ConcurrentPackAudit.Later.attestation = value.ConcurrentPackAudit.First.attestation
		}},
		{name: "reused exact host pointer", mutate: func(value *Evidence) {
			value.ConcurrentPackAudit.Later.host = value.ConcurrentPackAudit.First.host
		}},
		{name: "reused request identity", mutate: func(value *Evidence) {
			value.ConcurrentPackAudit.Later.RequestID = value.ConcurrentPackAudit.First.RequestID
		}},
		{name: "aliased snapshot identity", mutate: func(value *Evidence) {
			value.ConcurrentPackAudit.Later.SnapshotID = value.ConcurrentPackAudit.First.SnapshotID
		}},
		{name: "aliased pack identity", mutate: func(value *Evidence) {
			value.ConcurrentPackAudit.Later.EvidencePackID = value.ConcurrentPackAudit.First.EvidencePackID
		}},
		{name: "aliased pack digest", mutate: func(value *Evidence) {
			value.ConcurrentPackAudit.Later.PackDigest = value.ConcurrentPackAudit.First.PackDigest
		}},
		{name: "aliased content digest", mutate: func(value *Evidence) {
			value.ConcurrentPackAudit.Later.PackContentDigest = value.ConcurrentPackAudit.First.PackContentDigest
		}},
		{name: "missing identity proof", mutate: func(value *Evidence) { value.ConcurrentPackAudit.PackContentDiffers = false }},
		{name: "wrong first receipt", mutate: func(value *Evidence) { value.ConcurrentPackAudit.First.Isolation.ReceivedRequestID = "request-other" }},
		{name: "unchecked later cleanup", mutate: func(value *Evidence) { value.ConcurrentPackAudit.Later.Isolation.CleanupVerified = false }},
		{name: "later policy mutation", mutate: func(value *Evidence) { value.ConcurrentPackAudit.Later.Capability.ProbeScope = "tampered" }},
		{name: "later resource mutation", mutate: func(value *Evidence) { value.ConcurrentPackAudit.Later.Capability.ResourceLimits.Applied.MemoryBytes++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := base
			mutated.ConcurrentPackAudit = cloneConcurrentPackAudit(base.ConcurrentPackAudit)
			test.mutate(&mutated)
			if err := ValidateEvidence(mutated); err == nil {
				t.Fatalf("concurrent-pack audit mutation %q was accepted", test.name)
			}
		})
	}
}

func TestValidateA10RejectsConcurrentPackAuditJSONRoundTrip(t *testing.T) {
	base := RunA10(t)
	if base.ConcurrentPackAudit == nil {
		t.Fatal("A10 did not produce a concurrent-pack audit")
	}
	data, err := json.Marshal(base.ConcurrentPackAudit)
	if err != nil {
		t.Fatalf("marshal concurrent-pack audit: %v", err)
	}
	var roundTripped ConcurrentPackEditAudit
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("unmarshal concurrent-pack audit: %v", err)
	}
	if err := ValidateConcurrentPackEditAudit(base.SnapshotID, base.ComputedBasisID, base.GenerationID, base.PackDigest, roundTripped); err == nil {
		t.Fatal("JSON round-trip fabricated exact Core host authorities")
	}
}

func TestValidateEvidenceBindsPolicyAndResourceLimitEvidence(t *testing.T) {
	base := RunA03(t)
	for _, tc := range []struct {
		name   string
		mutate func(*Evidence)
	}{
		{name: "policy digest mismatch", mutate: func(value *Evidence) { value.CapabilityAudit.PolicyDigest = "sha256:" + strings.Repeat("c", 64) }},
		{name: "missing capability resource evidence", mutate: func(value *Evidence) { value.CapabilityAudit.ResourceLimits = nil }},
		{name: "applied resource limit mismatch", mutate: func(value *Evidence) { value.MountAudit.ResourceLimits.Applied.MemoryBytes++ }},
		{name: "capability and isolation resource mismatch", mutate: func(value *Evidence) { value.CapabilityAudit.ResourceLimits.Applied.ProcessCount++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := base
			mutated.RepositoryWriteAudit = append([]string(nil), base.RepositoryWriteAudit...)
			mutated.ObjectRefs = append([]string(nil), base.ObjectRefs...)
			mutated.MountAudit.ResourceLimits = cloneResourceEvidence(base.MountAudit.ResourceLimits)
			mutated.CapabilityAudit.ResourceLimits = cloneResourceEvidence(base.CapabilityAudit.ResourceLimits)
			tc.mutate(&mutated)
			if err := ValidateEvidence(mutated); err == nil {
				t.Fatalf("mutated policy/resource evidence was accepted")
			}
		})
	}
}

func TestValidateEvidenceBindsEveryRuntimePolicyIdentityField(t *testing.T) {
	base := RunA03(t)
	newDigest := "sha256:" + strings.Repeat("a", 64)
	tests := []struct {
		name   string
		mutate func(*Evidence)
	}{
		{name: "capability runtime policy binding", mutate: func(value *Evidence) { value.CapabilityAudit.RuntimePolicyBinding = "tampered_runtime_binding" }},
		{name: "capability runtime shared-base digest", mutate: func(value *Evidence) {
			value.CapabilityAudit.RuntimePolicySharedBaseDigest = newDigest
			value.CapabilityAudit.ProbeSharedBaseDigest = newDigest
		}},
		{name: "capability probe scope", mutate: func(value *Evidence) { value.CapabilityAudit.ProbeScope = "tampered_probe_scope" }},
		{name: "capability probe policy digest", mutate: func(value *Evidence) { value.CapabilityAudit.ProbePolicyDigest = newDigest }},
		{name: "capability probe shared-base digest", mutate: func(value *Evidence) {
			value.CapabilityAudit.RuntimePolicySharedBaseDigest = newDigest
			value.CapabilityAudit.ProbeSharedBaseDigest = newDigest
		}},
		{name: "isolation runtime policy binding", mutate: func(value *Evidence) { value.MountAudit.RuntimePolicyBinding = "tampered_runtime_binding" }},
		{name: "isolation runtime shared-base digest", mutate: func(value *Evidence) {
			value.MountAudit.RuntimePolicySharedBaseDigest = newDigest
			value.MountAudit.ProbeSharedBaseDigest = newDigest
		}},
		{name: "isolation probe scope", mutate: func(value *Evidence) { value.MountAudit.ProbeScope = "tampered_probe_scope" }},
		{name: "isolation probe policy digest", mutate: func(value *Evidence) { value.MountAudit.ProbePolicyDigest = newDigest }},
		{name: "isolation probe shared-base digest", mutate: func(value *Evidence) {
			value.MountAudit.RuntimePolicySharedBaseDigest = newDigest
			value.MountAudit.ProbeSharedBaseDigest = newDigest
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := base
			mutated.RepositoryWriteAudit = append([]string(nil), base.RepositoryWriteAudit...)
			mutated.ObjectRefs = append([]string(nil), base.ObjectRefs...)
			mutated.ModelHostLifecycle = append([]ModelHostObservation(nil), base.ModelHostLifecycle...)
			mutated.CapabilityAudit.ResourceLimits = cloneResourceEvidence(base.CapabilityAudit.ResourceLimits)
			mutated.MountAudit.ResourceLimits = cloneResourceEvidence(base.MountAudit.ResourceLimits)
			test.mutate(&mutated)
			if err := ValidateEvidence(mutated); err == nil {
				t.Fatalf("policy identity mutation %q was accepted", test.name)
			}
		})
	}
}

func TestValidateA10EvidenceBindsEveryRuntimePolicyIdentityField(t *testing.T) {
	base := RunA10(t)
	newDigest := "sha256:" + strings.Repeat("b", 64)
	tests := []struct {
		name   string
		mutate func(*ModelHostObservation)
	}{
		{name: "capability runtime policy binding", mutate: func(value *ModelHostObservation) { value.Capability.RuntimePolicyBinding = "tampered_runtime_binding" }},
		{name: "capability runtime shared-base digest", mutate: func(value *ModelHostObservation) {
			value.Capability.RuntimePolicySharedBaseDigest = newDigest
			value.Capability.ProbeSharedBaseDigest = newDigest
		}},
		{name: "capability probe scope", mutate: func(value *ModelHostObservation) { value.Capability.ProbeScope = "tampered_probe_scope" }},
		{name: "capability probe policy digest", mutate: func(value *ModelHostObservation) { value.Capability.ProbePolicyDigest = newDigest }},
		{name: "capability probe shared-base digest", mutate: func(value *ModelHostObservation) {
			value.Capability.RuntimePolicySharedBaseDigest = newDigest
			value.Capability.ProbeSharedBaseDigest = newDigest
		}},
		{name: "isolation runtime policy binding", mutate: func(value *ModelHostObservation) { value.Isolation.RuntimePolicyBinding = "tampered_runtime_binding" }},
		{name: "isolation runtime shared-base digest", mutate: func(value *ModelHostObservation) {
			value.Isolation.RuntimePolicySharedBaseDigest = newDigest
			value.Isolation.ProbeSharedBaseDigest = newDigest
		}},
		{name: "isolation probe scope", mutate: func(value *ModelHostObservation) { value.Isolation.ProbeScope = "tampered_probe_scope" }},
		{name: "isolation probe policy digest", mutate: func(value *ModelHostObservation) { value.Isolation.ProbePolicyDigest = newDigest }},
		{name: "isolation probe shared-base digest", mutate: func(value *ModelHostObservation) {
			value.Isolation.RuntimePolicySharedBaseDigest = newDigest
			value.Isolation.ProbeSharedBaseDigest = newDigest
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := base
			mutated.RepositoryWriteAudit = append([]string(nil), base.RepositoryWriteAudit...)
			mutated.ObjectRefs = append([]string(nil), base.ObjectRefs...)
			mutated.ModelHostLifecycle = append([]ModelHostObservation(nil), base.ModelHostLifecycle...)
			test.mutate(&mutated.ModelHostLifecycle[0])
			if err := ValidateEvidence(mutated); err == nil {
				t.Fatalf("A10 lifecycle policy identity mutation %q was accepted", test.name)
			}
		})
	}
}

func cloneResourceEvidence(value *protocol.ModelHostResourceLimitEvidence) *protocol.ModelHostResourceLimitEvidence {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func TestVerifyConcurrentPackEdit_RecordsTwoValidSnapshotPacks(t *testing.T) {
	f := newFixture(t)
	first := buildPack(t, f)
	observed := verifyConcurrentPackEditEvidence(t, f, first)
	if !observed.Verified {
		t.Fatal("concurrent pack edit was not verified")
	}
	if !observed.Audit.Verified() || observed.Audit.First.HostIdentity == observed.Audit.Later.HostIdentity || observed.Audit.First.RequestID == observed.Audit.Later.RequestID || observed.Audit.First.EvidencePackID == observed.Audit.Later.EvidencePackID {
		t.Fatalf("concurrent pack edit did not retain two independent structured executions: %+v", observed.Audit)
	}
	if err := semantic.ValidateEvidencePackV2(&observed.FirstPack); err != nil {
		t.Fatalf("first recorded pack is invalid: %v", err)
	}
	if err := semantic.ValidateEvidencePackV2(&observed.LaterPack); err != nil {
		t.Fatalf("later recorded pack is invalid: %v", err)
	}
	if observed.FirstPack.PackDigest != first.PackDigest || observed.FirstPack.Items[0].Content != first.Items[0].Content {
		t.Fatalf("first pack changed during later snapshot construction: %+v", observed.FirstPack)
	}
	if observed.LaterPack.SnapshotID == observed.FirstPack.SnapshotID || observed.LaterPack.ComputedBasisID == observed.FirstPack.ComputedBasisID || observed.LaterPack.GenerationID == observed.FirstPack.GenerationID || observed.LaterPack.PackDigest == observed.FirstPack.PackDigest || observed.LaterPack.Items[0].Content == observed.FirstPack.Items[0].Content {
		t.Fatalf("later valid snapshot pack did not have distinct immutable identities/content: first=%+v later=%+v", observed.FirstPack, observed.LaterPack)
	}
}

func TestRFLSCR2VS08ModelHostHelper(t *testing.T) {
	if os.Getenv("CODEFLOW_VS08_MODEL_HOST_HELPER") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		body, err := readVS08Frame(reader)
		if err != nil {
			return
		}
		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      string          `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			return
		}
		if request.Method == "initialize" {
			probe := vs08ModelHostFilesystemProbe()
			result := protocol.ModelHostResponse{SchemaID: protocol.ModelHostResponseSchemaID, SchemaVersion: protocol.ModelHostProtocolVersion, RequestID: request.ID, Status: "ok", Capability: protocol.ModelHostCapability{Status: "measured", ModelID: "vs08-fake", Revision: "test", License: "MIT", Checksum: "sha256:vs08-fake", Runtime: "go-test", DataBoundary: "bounded-pack", Measured: true, SchemaConstrained: true, Cancellation: true, MaxRequestBytes: protocol.DefaultMaxMessageSizeBytes, MaxResponseBytes: protocol.DefaultMaxMessageSizeBytes, IsolationBackend: "sandbox-exec", IsolationEnforced: true, IsolationProbe: &probe, NetworkPolicy: "deny_all"}}
			if !writeVS08Result(request.ID, result) {
				return
			}
			continue
		}
		if request.Method != protocol.ModelHostEnrichMethod {
			return
		}
		var params protocol.ModelHostRequest
		if err := json.Unmarshal(request.Params, &params); err != nil || len(params.EvidencePack) == 0 || strings.Contains(string(params.EvidencePack), "repoRoot") {
			return
		}
		if delay := os.Getenv("CODEFLOW_VS08_MODEL_HOST_DELAY"); strings.HasPrefix(delay, "before-receipt:") {
			if duration, parseErr := time.ParseDuration(strings.TrimPrefix(delay, "before-receipt:")); parseErr == nil {
				time.Sleep(duration)
			}
		}
		if !writeVS08Receipt(params.RequestID, params.PackDigest) {
			return
		}
		if recordPath := os.Getenv("CODEFLOW_VS08_MODEL_HOST_RECORD"); recordPath != "" {
			file, err := os.OpenFile(recordPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				return
			}
			_, _ = fmt.Fprintf(file, "%s|%s\n", params.PackDigest, params.EvidencePack)
			_ = file.Close()
		}
		if releasePath := os.Getenv("CODEFLOW_VS08_MODEL_HOST_RELEASE"); releasePath != "" {
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, statErr := os.Stat(releasePath); statErr == nil {
					break
				} else if !os.IsNotExist(statErr) || time.Now().After(deadline) {
					return
				}
				time.Sleep(2 * time.Millisecond)
			}
		}
		mode := os.Getenv("CODEFLOW_VS08_MODEL_HOST_MODE")
		switch mode {
		case "crash":
			os.Exit(17)
		case "failure":
			if !writeVS08Error(request.ID, -32001, "fake failure") {
				return
			}
			continue
		case "timeout", "cancel":
			time.Sleep(2 * time.Second)
			continue
		}
		if delay := os.Getenv("CODEFLOW_VS08_MODEL_HOST_DELAY"); delay != "" && !strings.HasPrefix(delay, "before-receipt:") {
			if duration, parseErr := time.ParseDuration(delay); parseErr == nil {
				time.Sleep(duration)
			}
		}
		result := protocol.ModelHostResponse{SchemaID: protocol.ModelHostResponseSchemaID, SchemaVersion: protocol.ModelHostProtocolVersion, RequestID: request.ID, Status: "accepted", Proposal: json.RawMessage(`{"schemaId":"https://codeflow.local/schemas/rflsc.semantic-proposal.v2.schema.json","schemaVersion":2}`)}
		if !writeVS08Result(request.ID, result) {
			return
		}
	}
}

const vs08ModelHostProbeSentinelContents = "codeflow-model-host-sentinel-v1\n"

func vs08ModelHostProbeSentinelPath() string {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	return filepath.Join(repoRoot, "internal", "protocol", "testdata", "model-host-sentinel.txt")
}

func vs08ModelHostFilesystemProbe() protocol.ModelHostIsolationProbe {
	path := vs08ModelHostProbeSentinelPath()
	expected := []byte(vs08ModelHostProbeSentinelContents)
	beforeBytes := expected
	readAttempt := "blocked"
	if data, err := os.ReadFile(path); err == nil {
		beforeBytes = data
		readAttempt = "allowed"
	}
	beforeSum := sha256.Sum256(beforeBytes)
	probe := protocol.ModelHostIsolationProbe{RepositoryReadAttempt: readAttempt, SentinelBeforeDigest: "sha256:" + hex.EncodeToString(beforeSum[:])}
	writeAttempt := "blocked"
	writePath := filepath.Join("/tmp", "codeflow-model-host-probe-write-"+strconv.Itoa(os.Getpid()))
	if file, err := os.OpenFile(writePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err == nil {
		writeAttempt = "allowed"
		_ = file.Close()
		_ = os.Remove(writePath)
	}
	probe.RepositoryWriteAttempt = writeAttempt
	probe.NetworkAttempt = "blocked"
	listener, listenErr := net.Listen("tcp4", "127.0.0.1:0")
	if listenErr == nil {
		defer listener.Close()
		connection, dialErr := net.DialTimeout("tcp4", listener.Addr().String(), 50*time.Millisecond)
		if dialErr == nil {
			probe.NetworkAttempt = "allowed"
			_ = connection.Close()
		}
	}
	afterBytes := beforeBytes
	if data, err := os.ReadFile(path); err == nil {
		afterBytes = data
	}
	afterSum := sha256.Sum256(afterBytes)
	probe.SentinelAfterDigest = "sha256:" + hex.EncodeToString(afterSum[:])
	probe.SentinelUnchanged = probe.SentinelBeforeDigest == probe.SentinelAfterDigest
	return probe
}

func readVS08Frame(reader *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			contentLength, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing content length")
	}
	body := make([]byte, contentLength)
	_, err := io.ReadFull(reader, body)
	return body, err
}

func writeVS08Result(id string, result protocol.ModelHostResponse) bool {
	body, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Result  any    `json:"result"`
	}{"2.0", id, result})
	if err != nil {
		return false
	}
	return writeVS08Frame(body)
}

func writeVS08Receipt(requestID, packDigest string) bool {
	body, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  struct {
			RequestID  string `json:"requestId"`
			PackDigest string `json:"packDigest"`
		} `json:"params"`
	}{JSONRPC: "2.0", Method: "request_received", Params: struct {
		RequestID  string `json:"requestId"`
		PackDigest string `json:"packDigest"`
	}{RequestID: requestID, PackDigest: packDigest}})
	if err != nil {
		return false
	}
	return writeVS08Frame(body)
}

func writeVS08Error(id string, code int, message string) bool {
	body, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Error   any    `json:"error"`
	}{"2.0", id, map[string]any{"code": code, "message": message}})
	if err != nil {
		return false
	}
	return writeVS08Frame(body)
}

func writeVS08Frame(body []byte) bool {
	if _, err := fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return false
	}
	_, err := os.Stdout.Write(body)
	return err == nil
}
