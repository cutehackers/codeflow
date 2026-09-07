package semantic

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"codeflow/internal/protocol"
)

func TestRunSemanticEnrichmentActualSpawnedHostReachesTypedAndEgress(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	snapshot, mapIR, request := enrichmentTestInput(t)
	pack, err := BuildEvidencePackV2(request)
	if err != nil {
		t.Fatal(err)
	}
	digests, err := CanonicalQ3Digests(mapIR)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := json.Marshal(ModelProposal{
		SchemaID: SemanticProposalV2SchemaID, SchemaVersion: 2, ProposalID: "proposal-actual-f22",
		ComputedBasisID: snapshot.ComputedBasisID, GenerationID: mapIR.GenerationID, SnapshotID: snapshot.SnapshotID,
		TargetStepID: "step-a03", TargetSymbolPath: "Submit", ProposedTitle: "Submit checkout",
		ProposedCategory: "entry", EpistemicStatus: "inferred", Authority: "model", ClaimScope: "display_only",
		ModelID: "actual-test-model", ModelRevision: "r1", PromptRevision: "prompt-actual-f22", SchemaProfile: SemanticProposalSchemaProfile, PackDigest: pack.PackDigest,
		EvidenceRefs: []string{"e-a03"}, FactDigest: digests.Fact, ObligationDigest: digests.Obligation,
		AlignmentDigest: digests.Alignment, SettlementDigest: digests.Settlement,
	})
	if err != nil {
		t.Fatal(err)
	}
	proposalEncoded := base64.RawStdEncoding.EncodeToString(proposal)
	host, err := protocol.SpawnModelHost(context.Background(), protocol.ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestSemanticModelHostHelper"},
		Env: []string{
			"CODEFLOW_MODEL_HOST_HELPER=semantic",
			"CODEFLOW_VS08_MODEL_HOST_RELEASE=" + proposalEncoded,
		},
		DefaultTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("spawn actual semantic model host: %v", err)
	}
	result := RunSemanticEnrichment(context.Background(), EnrichmentRequest{
		EvidencePack: request, ModelHostFactory: func(context.Context) (*protocol.ModelHost, error) { return host, nil }, PromptRevision: "prompt-actual-f22",
	})
	if result.State.Status != "available" || result.Pack == nil || result.Proposal == nil {
		t.Fatalf("actual supervised host result = %+v", result)
	}
	if result.State.Isolation.TerminalStatus != "success" || !result.State.Isolation.CleanupVerified {
		t.Fatalf("actual host lifecycle evidence = %+v", result.State.Isolation)
	}
	if result.State.Isolation.PackDigest != pack.PackDigest || result.State.Isolation.ReceivedRequestID == "" || result.State.Isolation.ReceivedPackDigest != pack.PackDigest {
		t.Fatalf("actual host receipt evidence = %+v", result.State.Isolation)
	}
	if result.State.Capability.PolicyDigest == "sha256:"+strings.Repeat("c", 64) || result.State.Capability.PolicyDigest != result.State.Isolation.PolicyDigest {
		t.Fatalf("child-provided policy digest was not replaced by Core policy identity: capability=%q isolation=%q", result.State.Capability.PolicyDigest, result.State.Isolation.PolicyDigest)
	}
	if err := ValidateEnrichmentResultContract(&result); err != nil {
		t.Fatalf("actual supervised result failed typed/schema validation: %v", err)
	}
	egress, err := MarshalEnrichmentResultEgress(&result)
	if err != nil {
		t.Fatalf("actual supervised result failed egress validation: %v", err)
	}
	var public struct {
		State    EnrichmentState `json:"state"`
		Proposal *ModelProposal  `json:"proposal"`
	}
	if err := json.Unmarshal(egress, &public); err != nil {
		t.Fatalf("decode actual egress: %v", err)
	}
	if public.State.Status != "available" || public.Proposal == nil || public.Proposal.ProposalID != "proposal-actual-f22" {
		t.Fatalf("actual public egress = %+v", public)
	}
	var roundTripped EnrichmentResult
	if err := json.Unmarshal(egress, &roundTripped); err != nil {
		t.Fatalf("decode egress into typed result: %v", err)
	}
	if err := ValidateEnrichmentResult(&roundTripped); err == nil {
		t.Fatal("JSON round-trip restored Core authority")
	} else if !strings.Contains(err.Error(), "Core-supervised host authority") {
		t.Fatalf("JSON round-trip failed for a reason other than missing Core authority: %v", err)
	}
}

func TestRunSemanticEnrichmentActualHostRepositoryWriteAttemptBlocksProposal(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	snapshot, mapIR, request := enrichmentTestInput(t)
	pack, err := BuildEvidencePackV2(request)
	if err != nil {
		t.Fatal(err)
	}
	digests, err := CanonicalQ3Digests(mapIR)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := json.Marshal(ModelProposal{
		SchemaID: SemanticProposalV2SchemaID, SchemaVersion: 2, ProposalID: "proposal-repository-write-attempt",
		ComputedBasisID: snapshot.ComputedBasisID, GenerationID: mapIR.GenerationID, SnapshotID: snapshot.SnapshotID,
		TargetStepID: "step-a03", TargetSymbolPath: "Submit", ProposedTitle: "Submit checkout",
		ProposedCategory: "entry", EpistemicStatus: "inferred", Authority: "model", ClaimScope: "display_only",
		ModelID: "actual-test-model", ModelRevision: "r1", PromptRevision: "prompt-repository-write-attempt", SchemaProfile: SemanticProposalSchemaProfile, PackDigest: pack.PackDigest,
		EvidenceRefs: []string{"e-a03"}, FactDigest: digests.Fact, ObligationDigest: digests.Obligation,
		AlignmentDigest: digests.Alignment, SettlementDigest: digests.Settlement,
	})
	if err != nil {
		t.Fatal(err)
	}
	proposalEncoded := base64.RawStdEncoding.EncodeToString(proposal)
	host, err := protocol.SpawnModelHost(context.Background(), protocol.ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestSemanticModelHostHelper"},
		Env: []string{
			"CODEFLOW_MODEL_HOST_HELPER=semantic",
			"CODEFLOW_MODEL_HOST_MODE=repository-write-attempt",
			"CODEFLOW_VS08_MODEL_HOST_RELEASE=" + proposalEncoded,
		},
		DefaultTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("spawn actual adversarial model host: %v", err)
	}
	result := RunSemanticEnrichment(context.Background(), EnrichmentRequest{
		EvidencePack: request, ModelHostFactory: func(context.Context) (*protocol.ModelHost, error) { return host, nil }, PromptRevision: "prompt-repository-write-attempt",
	})
	if result.State.Status != "unavailable" || !strings.Contains(result.State.Reason, "source_integrity_violation") {
		t.Fatalf("repository write attempt was not fail-closed: status=%q reason=%q", result.State.Status, result.State.Reason)
	}
	if result.Proposal != nil || result.View != nil {
		t.Fatalf("repository write attempt exposed model proposal: %+v", result)
	}
	evidence := host.IsolationEvidence()
	if evidence.RepositoryWriteAuditStatus != protocol.ModelHostRepositoryWriteAuditAttributed || len(evidence.RepositoryWriteAttempts) == 0 {
		t.Fatalf("Core did not attribute the runtime repository write attempt: %+v", evidence)
	}
	if evidence.SentinelBeforeDigest == "" || evidence.SentinelBeforeDigest != evidence.SentinelAfterDigest || !evidence.SentinelUnchanged {
		t.Fatalf("repository sentinel integrity was not preserved: %+v", evidence)
	}
	if !evidence.CleanupVerified {
		t.Fatalf("adversarial host cleanup was not verified: %+v", evidence)
	}
}

func TestRunSemanticEnrichmentActualHostNoReceiptTimeoutIsLifecycleUnavailable(t *testing.T) {
	testRunActualNoReceiptLifecycle(t, "no-receipt-timeout", false)
}

func TestRunSemanticEnrichmentActualHostNoReceiptCancellationIsLifecycleUnavailable(t *testing.T) {
	testRunActualNoReceiptLifecycle(t, "no-receipt-cancel", true)
}

func testRunActualNoReceiptLifecycle(t *testing.T, mode string, cancelRequest bool) {
	t.Helper()
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	_, mapIR, request := enrichmentTestInput(t)
	beforeDigests, err := CanonicalQ3Digests(mapIR)
	if err != nil {
		t.Fatal(err)
	}
	release := base64.RawStdEncoding.EncodeToString([]byte(`{}`))
	host, err := protocol.SpawnModelHost(context.Background(), protocol.ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestSemanticModelHostHelper"},
		Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=semantic", "CODEFLOW_MODEL_HOST_MODE=" + mode, "CODEFLOW_VS08_MODEL_HOST_RELEASE=" + release},
		DefaultTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("spawn no-receipt model host: %v", err)
	}

	ctx := context.Background()
	var cancel context.CancelFunc
	if cancelRequest {
		ctx, cancel = context.WithCancel(ctx)
		timer := time.AfterFunc(150*time.Millisecond, cancel)
		defer timer.Stop()
	} else {
		ctx, cancel = context.WithTimeout(ctx, 150*time.Millisecond)
		defer cancel()
	}
	result := RunSemanticEnrichment(ctx, EnrichmentRequest{
		EvidencePack: request,
		ModelHostFactory: func(context.Context) (*protocol.ModelHost, error) {
			return host, nil
		},
	})
	if cancelRequest {
		if result.State.Status != "unavailable" || !strings.Contains(result.State.Reason, "cancelled") {
			t.Fatalf("no-receipt cancellation result = %+v, want canonical unavailable cancellation", result)
		}
	} else if result.State.Status != "timed_out" {
		t.Fatalf("no-receipt timeout result = %+v, want timed_out", result)
	}
	if strings.Contains(result.State.Reason, "source_integrity_violation") {
		t.Fatalf("legitimate no-receipt lifecycle was classified as source integrity violation: %+v", result)
	}
	if result.Proposal != nil || result.View != nil {
		t.Fatalf("no-receipt lifecycle exposed proposal: %+v", result)
	}
	evidence := result.State.Isolation
	if evidence.ReceivedRequestID != "" || evidence.ReceivedPackDigest != "" {
		t.Fatalf("no-receipt lifecycle fabricated receipt evidence: %+v", evidence)
	}
	wantTerminal := "timeout"
	if cancelRequest {
		wantTerminal = "cancel"
	}
	if evidence.TerminalStatus != wantTerminal || !evidence.CleanupVerified {
		t.Fatalf("no-receipt lifecycle evidence = %+v, want terminal %q and verified cleanup", evidence, wantTerminal)
	}
	if result.Fallback == nil || result.State.Fallback == nil {
		t.Fatalf("no-receipt lifecycle lost deterministic fallback: %+v", result)
	}
	afterDigests, err := CanonicalQ3Digests(mapIR)
	if err != nil {
		t.Fatal(err)
	}
	if beforeDigests != afterDigests {
		t.Fatalf("no-receipt lifecycle changed deterministic Q3 digests: before=%+v after=%+v", beforeDigests, afterDigests)
	}
	if err := ValidateEnrichmentResultContract(&result); err != nil {
		t.Fatalf("no-receipt lifecycle result failed contract validation: %v", err)
	}
	if _, err := MarshalEnrichmentResultEgress(&result); err != nil {
		t.Fatalf("no-receipt lifecycle egress failed validation: %v", err)
	}
}

func TestSemanticModelHostHelper(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") != "semantic" {
		return
	}
	proposal, err := base64.RawStdEncoding.DecodeString(os.Getenv("CODEFLOW_VS08_MODEL_HOST_RELEASE"))
	if err != nil {
		return
	}
	reader := bufio.NewReaderSize(os.Stdin, 64<<10)
	for {
		body, err := readSemanticModelHostFrame(reader)
		if err != nil {
			return
		}
		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      string          `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if json.Unmarshal(body, &request) != nil || request.JSONRPC != "2.0" {
			return
		}
		switch request.Method {
		case "initialize":
			probe := &protocol.ModelHostIsolationProbe{
				RepositoryReadAttempt: "blocked", RepositoryWriteAttempt: "blocked",
				DisposableWriteAttempt: "blocked",
				SentinelBeforeDigest:   "sha256:" + strings.Repeat("a", 64), SentinelAfterDigest: "sha256:" + strings.Repeat("a", 64),
				SentinelUnchanged: true, NetworkAttempt: "blocked",
			}
			capability := protocol.ModelHostCapability{
				Status: "measured", ModelID: "actual-test-model", Revision: "r1", License: "MIT", Checksum: "sha256:actual", Runtime: "test", DataBoundary: "local-only",
				Measured: true, SchemaConstrained: true, Cancellation: true, MaxRequestBytes: protocol.DefaultMaxMessageSizeBytes, MaxResponseBytes: protocol.DefaultMaxMessageSizeBytes,
				IsolationBackend: "sandbox-exec", IsolationEnforced: true, IsolationProbe: probe, NetworkPolicy: "deny_all", PolicyDigest: "sha256:" + strings.Repeat("c", 64),
			}
			result := protocol.ModelHostResponse{SchemaID: protocol.ModelHostResponseSchemaID, SchemaVersion: 2, RequestID: request.ID, Status: "ok", Capability: capability}
			if writeSemanticModelHostResponse(request.ID, result) != nil {
				return
			}
		case protocol.ModelHostEnrichMethod:
			var params protocol.ModelHostRequest
			if json.Unmarshal(request.Params, &params) != nil {
				return
			}
			mode := os.Getenv("CODEFLOW_MODEL_HOST_MODE")
			if mode != "no-receipt-timeout" && mode != "no-receipt-cancel" {
				if writeSemanticModelHostReceipt(params.RequestID, params.PackDigest) != nil {
					return
				}
			}
			if mode == "no-receipt-timeout" || mode == "no-receipt-cancel" {
				for {
					time.Sleep(time.Second)
				}
			}
			if mode == "repository-write-attempt" {
				if !modelHostAttemptRepositoryWriteAndNotifyCore(params.RequestID) {
					return
				}
			}
			result := protocol.ModelHostResponse{SchemaID: protocol.ModelHostResponseSchemaID, SchemaVersion: 2, RequestID: params.RequestID, Status: "accepted", Proposal: json.RawMessage(proposal)}
			if writeSemanticModelHostResponse(request.ID, result) != nil {
				return
			}
		default:
			return
		}
	}
}

// modelHostAttemptRepositoryWriteAndNotifyCore is test-only adversarial code.
// It obtains the source location from the test binary's compiled callsite,
// rather than from the model-host request, argv, or environment. The model
// host therefore attempts a real O_WRONLY open against the guessed repository
// sentinel while Core's sandbox remains the authority for whether that open
// is permitted. The FIFO notification is conservative attribution only. Its
// absence is never used as proof that an unobserved source attempt was absent.
func modelHostAttemptRepositoryWriteAndNotifyCore(requestID string) bool {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return false
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	sentinelPath := filepath.Join(repositoryRoot, ".git", "HEAD")
	if info, err := os.Stat(filepath.Join(repositoryRoot, ".git")); err == nil && !info.IsDir() {
		sentinelPath = filepath.Join(repositoryRoot, ".git")
	}
	// Attempt the source open regardless of whether the sandbox denies it. A
	// successful open is still reported conservatively and is caught by the
	// Core-owned before/after sentinel and policy checks.
	if file, err := os.OpenFile(sentinelPath, os.O_WRONLY, 0); err == nil {
		_ = file.Close()
	}
	challenge, err := os.ReadFile(".codeflow-model-host-repository-write-challenge-v1")
	if err != nil {
		return false
	}
	target, err := os.OpenFile(".codeflow-model-host-repository-write-target-v1", os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	defer target.Close()
	_, err = target.Write([]byte(strings.TrimSpace(string(challenge)) + "\x00" + requestID))
	return err == nil
}

func readSemanticModelHostFrame(reader *bufio.Reader) ([]byte, error) {
	contentLength := int64(-1)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			continue
		}
		if contentLength >= 0 {
			return nil, fmt.Errorf("duplicate content length")
		}
		contentLength, err = strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || contentLength < 0 || contentLength > protocol.DefaultMaxMessageSizeBytes {
			return nil, fmt.Errorf("invalid content length")
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing content length")
	}
	body := make([]byte, contentLength)
	_, err := io.ReadFull(reader, body)
	return body, err
}

func writeSemanticModelHostResponse(id string, result protocol.ModelHostResponse) error {
	body, err := json.Marshal(struct {
		JSONRPC string                     `json:"jsonrpc"`
		ID      string                     `json:"id"`
		Result  protocol.ModelHostResponse `json:"result"`
	}{JSONRPC: "2.0", ID: id, Result: result})
	if err != nil {
		return err
	}
	return writeSemanticModelHostFrame(body)
}

func writeSemanticModelHostReceipt(requestID, packDigest string) error {
	var envelope struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  struct {
			RequestID  string `json:"requestId"`
			PackDigest string `json:"packDigest"`
		} `json:"params"`
	}
	envelope.JSONRPC, envelope.Method = "2.0", "request_received"
	envelope.Params.RequestID, envelope.Params.PackDigest = requestID, packDigest
	body, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	return writeSemanticModelHostFrame(body)
}

func writeSemanticModelHostFrame(body []byte) error {
	if int64(len(body)) > protocol.DefaultMaxMessageSizeBytes {
		return fmt.Errorf("frame exceeds bound")
	}
	if _, err := fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err := os.Stdout.Write(body)
	return err
}
