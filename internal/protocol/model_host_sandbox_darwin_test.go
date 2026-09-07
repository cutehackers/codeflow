//go:build darwin

package protocol

import (
	"strings"
	"testing"
)

func TestModelHostSandboxProfilesSeparateProbePermissions(t *testing.T) {
	workDir := t.TempDir()
	repositoryRoot := "/repository-under-test"
	runtimeProfilePath := workDir + "/model-host.sb"
	probeProfilePath := workDir + "/model-host-probe.sb"
	modelBinary := "/usr/local/bin/model-host"
	probeBinaries := []string{"/bin/sh", "/usr/bin/head", "/usr/bin/nc"}
	runtimeProfile := modelHostSandboxProfileWithProbesAndDeniedPaths(modelBinary, workDir, repositoryRoot, runtimeProfilePath, []string{modelBinary}, nil, []string{runtimeProfilePath, probeProfilePath})
	probeProfile := modelHostSandboxProfileWithProbesAndDeniedPaths(probeBinaries[0], workDir, repositoryRoot, probeProfilePath, probeBinaries, probeBinaries, []string{probeProfilePath, runtimeProfilePath})
	if !strings.Contains(runtimeProfile, `(deny process-fork)`) || strings.Contains(runtimeProfile, `(allow process-fork)`) || strings.Contains(runtimeProfile, `(allow process*)`) || strings.Contains(runtimeProfile, `(allow process-exec (literal "/bin/sh"))`) || strings.Contains(runtimeProfile, `(allow process-exec (literal "/usr/bin/head"))`) || strings.Contains(runtimeProfile, `(allow process-exec (literal "/usr/bin/nc"))`) {
		t.Fatal("runtime profile exposes trusted probe executables")
	}
	for _, binary := range probeBinaries {
		if !strings.Contains(probeProfile, `(allow process-exec (literal "`+binary+`"))`) {
			t.Fatalf("probe profile does not allow trusted executable %q", binary)
		}
	}
	if modelHostSandboxBaseDigest(runtimeProfile) != modelHostSandboxBaseDigest(probeProfile) {
		t.Fatal("runtime and probe profiles do not share the same deny-base digest")
	}
	if modelHostSandboxPolicyDigest(runtimeProfile) == modelHostSandboxPolicyDigest(probeProfile) {
		t.Fatal("runtime and probe profiles unexpectedly share an exact policy digest")
	}
}

func TestModelHostSandboxPolicyBindingsRejectTampering(t *testing.T) {
	workDir := t.TempDir()
	repositoryRoot := "/repository-under-test"
	runtimeProfilePath := workDir + "/model-host.sb"
	probeProfilePath := workDir + "/model-host-probe.sb"
	modelBinary := "/usr/local/bin/model-host"
	probeBinaries := []string{"/bin/sh", "/usr/bin/head", "/usr/bin/nc", "/bin/test"}
	runtimeProfile := modelHostSandboxProfileWithProbesAndDeniedPaths(modelBinary, workDir, repositoryRoot, runtimeProfilePath, []string{modelBinary}, nil, []string{runtimeProfilePath, probeProfilePath})
	probeProfile := modelHostSandboxProfileWithProbesAndDeniedPaths(probeBinaries[0], workDir, repositoryRoot, probeProfilePath, probeBinaries, probeBinaries, []string{probeProfilePath, runtimeProfilePath})
	runtimeDigest := modelHostSandboxPolicyDigest(runtimeProfile)
	probeDigest := modelHostSandboxPolicyDigest(probeProfile)
	sharedDigest := modelHostSandboxSharedBaseDigest(runtimeProfile)

	if err := validateModelHostSandboxPolicyDigest(runtimeProfile+"\n", runtimeDigest); err == nil {
		t.Fatal("tampered runtime profile bytes were accepted")
	}
	if err := validateModelHostSandboxPolicyDigest(probeProfile+"\n", probeDigest); err == nil {
		t.Fatal("tampered probe profile bytes were accepted")
	}
	if err := validateModelHostSandboxRuntimeBinding("/usr/bin/sandbox-exec", []string{"-f", workDir + "/other.sb", modelBinary}, runtimeProfilePath, modelBinary); err == nil {
		t.Fatal("tampered runtime profile path was accepted")
	}
	if err := validateModelHostSandboxRuntimeBinding("/usr/bin/sandbox-exec", []string{"-f", runtimeProfilePath, "/usr/local/bin/other-model-host"}, runtimeProfilePath, modelBinary); err == nil {
		t.Fatal("tampered runtime executable argument was accepted")
	}

	evidence := modelHostPolicyEvidenceForTest(runtimeDigest, sharedDigest, probeDigest)
	capability := modelHostPolicyCapabilityForTest(evidence)
	validate := func(value ModelHostIsolationEvidence) error {
		return ValidateModelHostIsolationEvidenceForRequest(value, capability, "request-policy-binding", evidence.PackDigest, true)
	}
	for _, test := range []struct {
		name   string
		mutate func(*ModelHostIsolationEvidence)
	}{
		{name: "runtime digest", mutate: func(value *ModelHostIsolationEvidence) { value.PolicyDigest = "sha256:" + strings.Repeat("a", 64) }},
		{name: "probe digest", mutate: func(value *ModelHostIsolationEvidence) { value.ProbePolicyDigest = "sha256:" + strings.Repeat("b", 64) }},
		{name: "probe scope", mutate: func(value *ModelHostIsolationEvidence) { value.ProbeScope = "exact_runtime_profile" }},
		{name: "probe shared digest", mutate: func(value *ModelHostIsolationEvidence) {
			value.ProbeSharedBaseDigest = "sha256:" + strings.Repeat("c", 64)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			mutated := evidence
			test.mutate(&mutated)
			if err := validate(mutated); err == nil {
				t.Fatal("tampered policy evidence was accepted")
			}
		})
	}
}

func TestModelHostSandboxProfileRejectsUnexpectedClausesWithRecomputedDigest(t *testing.T) {
	workDir := t.TempDir()
	repositoryRoot := "/repository-under-test"
	runtimeProfilePath := workDir + "/model-host.sb"
	probeProfilePath := workDir + "/model-host-probe.sb"
	modelBinary := "/usr/local/bin/model-host"
	deniedPaths := []string{runtimeProfilePath, probeProfilePath}
	profile := modelHostSandboxProfileWithProbesAndDeniedPaths(modelBinary, workDir, repositoryRoot, runtimeProfilePath, []string{modelBinary}, nil, deniedPaths)
	cases := []struct {
		name   string
		mutate func(string) string
	}{
		{
			name: "unexpected literal read allow",
			mutate: func(value string) string {
				return strings.Replace(value, "(allow process-exec", "(allow file-read* (literal \"/tmp/unexpected-read\"))\n(allow process-exec", 1)
			},
		},
		{
			name: "unexpected process exec allow",
			mutate: func(value string) string {
				return strings.Replace(value, "(allow process-exec", "(allow process-exec (literal \"/usr/bin/true\"))\n(allow process-exec", 1)
			},
		},
		{
			name: "missing exact deny",
			mutate: func(value string) string {
				clause := "(deny file-read* (literal \"" + modelHostSandboxLiteral(runtimeProfilePath) + "\"))\n"
				return strings.Replace(value, clause, "", 1)
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			mutated := test.mutate(profile)
			if mutated == profile {
				t.Fatal("test mutation did not change profile")
			}
			mutatedDigest := modelHostSandboxPolicyDigest(mutated)
			if err := validateModelHostSandboxPolicyDigest(mutated, mutatedDigest); err != nil {
				t.Fatalf("mutated profile does not have its recomputed exact digest: %v", err)
			}
			if err := validateModelHostSandboxProfileWithProbesAndDeniedPaths(mutated, modelBinary, workDir, repositoryRoot, runtimeProfilePath, nil, deniedPaths, []string{modelBinary}); err == nil {
				t.Fatal("mutated sandbox profile was accepted after recomputing its exact digest")
			}
		})
	}
}

func modelHostPolicyEvidenceForTest(runtimeDigest, sharedDigest, probeDigest string) ModelHostIsolationEvidence {
	limits := DefaultModelHostResourceLimits()
	applied := limits
	applied.ProcessCount = modelHostResourceProcessTreeBound
	return ModelHostIsolationEvidence{
		SourceDelivery: "bounded_evidence_pack", SourceMount: "not_mounted", WorkingDirectoryMode: "process_private_disposable", WorkingDirectoryPermission: "0700", Disposable: true,
		RepositoryWriteAttempts: []string{}, RepositoryWriteAuditStatus: ModelHostRepositoryWriteAuditCapabilityEnforced, PackDigest: strings.Repeat("d", 64), ReceivedRequestID: "request-policy-binding", ReceivedPackDigest: strings.Repeat("d", 64), CapabilityStatus: "measured", TerminalStatus: "success", CleanupVerified: true,
		IsolationBackend: "sandbox-exec", EnforcementStatus: "enforced", RepositoryReadAttempt: "blocked", RepositoryWriteAttempt: "blocked", DisposableWriteAttempt: "blocked", SentinelBeforeDigest: "sha256:" + strings.Repeat("e", 64), SentinelAfterDigest: "sha256:" + strings.Repeat("e", 64), SentinelUnchanged: true, NetworkAttempt: "blocked", NetworkPolicy: "deny_all",
		PolicyDigest: runtimeDigest, RuntimePolicyBinding: ModelHostRuntimePolicyBindingExact, RuntimePolicySharedBaseDigest: sharedDigest, ProbeScope: ModelHostProbeScopeSharedDenyBase, ProbePolicyDigest: probeDigest, ProbeSharedBaseDigest: sharedDigest, CoreTrustedProbe: true,
		ResourceLimits: &ModelHostResourceLimitEvidence{Version: ModelHostResourceLimitsVersion, Declared: limits, Applied: applied, EnforcementStatus: ModelHostResourceEnforcementEnforced, Backend: ModelHostResourceBackendDarwinHostTree},
	}
}

func modelHostPolicyCapabilityForTest(evidence ModelHostIsolationEvidence) ModelHostCapability {
	probe := &ModelHostIsolationProbe{RepositoryReadAttempt: evidence.RepositoryReadAttempt, RepositoryWriteAttempt: evidence.RepositoryWriteAttempt, DisposableWriteAttempt: evidence.DisposableWriteAttempt, SentinelBeforeDigest: evidence.SentinelBeforeDigest, SentinelAfterDigest: evidence.SentinelAfterDigest, SentinelUnchanged: evidence.SentinelUnchanged, NetworkAttempt: evidence.NetworkAttempt}
	return ModelHostCapability{
		Status: "measured", ModelID: "model-host", Revision: "r1", License: "MIT", Checksum: "sha256:model-host", Runtime: "sandbox", DataBoundary: "local-only", Measured: true, SchemaConstrained: true, Cancellation: true,
		MaxRequestBytes: DefaultMaxMessageSizeBytes, MaxResponseBytes: DefaultMaxMessageSizeBytes, IsolationBackend: evidence.IsolationBackend, IsolationEnforced: true, IsolationProbe: probe, NetworkPolicy: evidence.NetworkPolicy, PolicyDigest: evidence.PolicyDigest,
		RuntimePolicyBinding: evidence.RuntimePolicyBinding, RuntimePolicySharedBaseDigest: evidence.RuntimePolicySharedBaseDigest, ProbeScope: evidence.ProbeScope, ProbePolicyDigest: evidence.ProbePolicyDigest, ProbeSharedBaseDigest: evidence.ProbeSharedBaseDigest, ResourceLimits: cloneModelHostResourceLimitEvidence(evidence.ResourceLimits),
	}
}
