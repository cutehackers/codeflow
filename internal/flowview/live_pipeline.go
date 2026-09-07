package flowview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"codeflow/internal/contractharness"
	"codeflow/internal/detect"
	"codeflow/internal/harvest"
	"codeflow/internal/protocol"
	"codeflow/internal/rflscvs02"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/storage"
	"codeflow/internal/workspace"
)

type liveRequest struct {
	query       *semantic.TaskViewQuery
	requestText string
}

// liveState is intentionally held separately from the durable map cache. A
// task query is the input to the always-on checkpoint consumer and is never a
// publication authority by itself.
type liveState struct {
	mu      sync.RWMutex
	request *liveRequest
}

// candidateCompiler is the immutable protocol-snapshot analysis seam used by
// the live consumer. The default implementation below runs the real adapter
// pipeline. Tests may replace this seam to exercise cancellation without
// introducing a second source model.
type candidateCompiler func(context.Context, protocol.Snapshot, *semantic.TaskViewQuery) (*semantic.SemanticMapIR, *semantic.FlowViewProjection, *slicing.SlicedPayload, *semantic.ResolvedTarget, *semantic.TaskIntent, *semantic.CausalObservationClosure, error)

func defaultLiveIdentity(repoRoot string) workspace.WorkspaceIdentity {
	abs, _ := filepath.Abs(repoRoot)
	h := sha256.Sum256([]byte(abs))
	digest := hex.EncodeToString(h[:])
	return workspace.WorkspaceIdentity{RepositoryID: "repo-" + digest[:24], WorktreeID: "worktree-" + digest[24:], ConfigurationFingerprint: "config-" + digest[:32]}
}

func queryIdentity(q *semantic.TaskViewQuery) string {
	if q == nil {
		return ""
	}
	b, err := json.Marshal(q)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *Server) rememberLiveRequest(q *semantic.TaskViewQuery, text string) {
	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	copyQuery := *q
	if q.Feature != nil {
		f := *q.Feature
		copyQuery.Feature = &f
	}
	s.live.request = &liveRequest{query: &copyQuery, requestText: text}
}

func (s *Server) latestLiveRequest() *liveRequest {
	s.live.mu.RLock()
	defer s.live.mu.RUnlock()
	if s.live.request == nil {
		return nil
	}
	copyReq := *s.live.request
	if s.live.request.query != nil {
		q := *s.live.request.query
		if q.Feature != nil {
			f := *q.Feature
			q.Feature = &f
		}
		copyReq.query = &q
	}
	return &copyReq
}

// compileSnapshotCandidate is the same adapter/snapshot/compiler path used by
// the background checkpoint consumer. It accepts only the immutable protocol
// snapshot and never falls back to repository reads.
func (s *Server) compileSnapshotCandidate(ctx context.Context, snapshot protocol.Snapshot, query *semantic.TaskViewQuery) (*semantic.SemanticMapIR, *semantic.FlowViewProjection, *slicing.SlicedPayload, *semantic.ResolvedTarget, *semantic.TaskIntent, *semantic.CausalObservationClosure, error) {
	if s.compileCandidate != nil {
		return s.compileCandidate(ctx, snapshot, query)
	}
	if query == nil || query.Feature == nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("missing_precondition: feature query is required")
	}
	det := detect.DetectSnapshot(snapshot.Files)
	lang := det.Language
	if lang == "" || lang == "unknown" {
		lang = "typescript"
	}
	adapterCfg, err := harvest.ResolveAdapter(lang, "")
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("resolve adapter: %w", err)
	}
	pool := protocol.NewPool(adapterCfg, 2)
	defer pool.Close()
	harvester := harvest.NewRunnerWithPool(pool)
	candidates, err := harvester.RunWithSnapshot(ctx, s.repoRoot, snapshot)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("harvest candidates: %w", err)
	}
	resolved, err := semantic.ResolveFeatureQueryTarget(query, candidates)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	slicePayload, err := slicing.NewRunner(pool).SliceWithSnapshot(ctx, s.repoRoot, resolved.CandidateID, resolved.EntrySymbolPath, nil, snapshot)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("slice: %w", err)
	}
	requestText := strings.TrimSpace(query.Feature.Request)
	if requestText == "" {
		requestText = resolved.Title
	}
	intent, err := semantic.NormalizeTaskIntent(requestText, semantic.IntentOptions{Mode: query.Mode})
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("normalize intent: %w", err)
	}
	snapshotInput, err := snapshot.AnalyzerInput()
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("snapshot input: %w", err)
	}
	queryHash := queryIdentity(query)
	// The adapter's observation closure is enriched with the exact query
	// identity at the core boundary. The digest is recomputed after enrichment.
	closure := &semantic.CausalObservationClosure{}
	if len(slicePayload.CausalObservationClosure) == 0 {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("missing_precondition: adapter closure is missing")
	}
	analysisResult := slicePayload.ValidatedResult
	if analysisResult == nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("missing_precondition: validated VS-02 analyzer result is missing")
	}
	resultBytes, err := json.Marshal(analysisResult)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("marshal validated analyzer result: %w", err)
	}
	if err := contractharness.Validate(rflscvs02.AnalyzerResultSchemaID, resultBytes); err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("analyzer result contract: %w", err)
	}
	analysisRequest, err := rflscvs02.NewAnalyzerRequest(analysisResult.RequestID, analysisResult.Operation, snapshotInput, nil, analysisResult.Closure.RequiredObservations)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("analyzer request identity: %w", err)
	}
	if err := rflscvs02.ValidateResult(analysisRequest, *analysisResult); err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("analyzer result semantic contract: %w", err)
	}
	*closure = semanticClosureFromVS02(analysisResult, snapshotInput.ConfigurationFingerprint)
	closure.TaskIntentRevision = intent.Revision
	closure.NormalizedQueryHash = queryHash
	if closure.ClosureDigest == "" {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("missing_precondition: analyzer closure digest is missing")
	}
	mapIR, projection, err := semantic.CompileDeterministicFeatureMap(resolved, intent, slicePayload, semantic.CompileOptions{
		ComputedBasisID: snapshot.ComputedBasisID, WorkspaceEpoch: snapshot.WorkspaceEpoch, ValidatedAgainstSnapshotID: snapshot.SnapshotID, SnapshotID: snapshot.SnapshotID, SnapshotTreeID: snapshot.RootTreeID,
		RepositoryID: snapshot.RepositoryID, WorktreeID: snapshot.WorktreeID, DependencyFingerprint: snapshot.DependencyFingerprint, ConfigurationFingerprint: snapshot.ConfigurationFingerprint,
		AdapterVersion: slicePayload.AdapterVersion, AnalyzerRevision: slicePayload.AnalyzerVersion, AnalysisReadSetID: closure.AnalysisReadSetID, CausalObservationClosureID: closure.ClosureID,
		SnapshotFiles: snapshot.Files, SnapshotInput: &snapshotInput,
	})
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("compile map: %w", err)
	}
	mapIR.Basis.WorktreeID = snapshot.WorktreeID
	mapIR.Basis.DependencyFingerprint = snapshot.DependencyFingerprint
	mapIR.Basis.CausalObservationClosureID = closure.ClosureID
	mapIR.Basis.AnalysisReadSetID = closure.AnalysisReadSetID
	mapBytes, err := json.Marshal(mapIR)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	if err := contractharness.ValidateSemanticMapIR(mapBytes); err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("semantic map contract: %w", err)
	}
	return mapIR, projection, slicePayload, resolved, intent, closure, nil
}

func semanticClosureFromVS02(result *rflscvs02.Result, configurationFingerprint string) semantic.CausalObservationClosure {
	closure := semantic.CausalObservationClosure{
		SchemaID:               semantic.ObservationClosureSchemaID,
		SchemaVersion:          semantic.SemanticSchemaVersion,
		ClosureID:              result.Closure.ClosureID,
		ComputedBasisID:        result.Closure.ComputedBasisID,
		WorkspaceEpoch:         result.Closure.WorkspaceEpoch,
		AnalysisReadSetID:      result.Closure.AnalysisReadSetID,
		RequiredObservations:   append([]string(nil), result.Closure.RequiredObservations...),
		MeasuredObservations:   append([]string(nil), result.Closure.MeasuredObservations...),
		ClosureStatus:          result.Closure.Status,
		IncompleteReasons:      append([]string(nil), result.Closure.IncompleteReasons...),
		ClosureDigest:          result.Closure.ClosureDigest,
		CanonicalResult:        result,
		PositiveDependencies:   semantic.PositiveDependencies{ConfigurationFingerprint: configurationFingerprint, DocumentRevisionRefs: make([]string, 0, len(result.ReadSet.Documents))},
		NegativeObservations:   make([]semantic.NegativeObservation, 0, len(result.Closure.NegativeObservations)),
		MembershipObservations: make([]semantic.MembershipObservation, 0, len(result.Closure.MembershipObservations)),
		DependencyFrontiers:    make([]semantic.DependencyFrontier, 0, len(result.Closure.DependencyFrontiers)),
	}
	for _, doc := range result.ReadSet.Documents {
		ref := doc.Path
		if doc.DocumentRevisionID != "" {
			ref += "@" + doc.DocumentRevisionID
		}
		closure.PositiveDependencies.DocumentRevisionRefs = append(closure.PositiveDependencies.DocumentRevisionRefs, ref)
	}
	for _, observation := range result.Closure.NegativeObservations {
		closure.NegativeObservations = append(closure.NegativeObservations, semantic.NegativeObservation{Kind: observation.Kind, Selector: observation.Path, ScopeRef: observation.Path, ObservedAgainstIndexRevision: observation.ValueHash})
	}
	for _, observation := range result.Closure.MembershipObservations {
		closure.MembershipObservations = append(closure.MembershipObservations, semantic.MembershipObservation{Kind: observation.Kind, ContainerRef: observation.Path, MembershipDigest: observation.ValueHash})
	}
	for _, observation := range result.Closure.DependencyFrontiers {
		closure.DependencyFrontiers = append(closure.DependencyFrontiers, semantic.DependencyFrontier{Direction: observation.Kind, RootRef: observation.Path, BoundaryRef: observation.Path, GraphRevision: observation.ValueHash})
	}
	if result.Capability.Adapter != "" {
		closure.CapabilityProfile = &semantic.CapabilityProfile{Adapter: result.Capability.Adapter, Features: append([]string(nil), result.Capability.Features...)}
	}
	if result.Coverage.Measured {
		closure.CoverageBoundary = &semantic.CoverageBoundary{IncludedSourceRoots: append([]string(nil), result.Coverage.IncludedSourceRoots...), ExcludedReasons: append([]string(nil), result.Coverage.ExcludedReasons...)}
	}
	return closure
}

// canonicalPublicationArtifacts serializes the complete evidence bundle that
// makes a current publication restart-verifiable. Callers do not supply CAS
// references or digests here. Each reference is derived from the exact bytes
// that are staged in the same storage transaction as the manifest and pointer.
func canonicalPublicationArtifacts(mapBytes []byte, projection *semantic.FlowViewProjection, result *rflscvs02.Result, semanticDeltas ...*semantic.SemanticDeltaIR) (storage.ArtifactRefs, map[string][]byte, map[string]string, error) {
	if len(mapBytes) == 0 || projection == nil || result == nil {
		return storage.ArtifactRefs{}, nil, nil, fmt.Errorf("complete publication artifact bundle is required")
	}
	if len(semanticDeltas) > 1 {
		return storage.ArtifactRefs{}, nil, nil, fmt.Errorf("at most one semantic delta artifact is supported")
	}
	if err := contractharness.ValidateSemanticMapIR(mapBytes); err != nil {
		return storage.ArtifactRefs{}, nil, nil, fmt.Errorf("semantic map artifact contract: %w", err)
	}
	projectionBytes, err := json.Marshal(projection)
	if err != nil {
		return storage.ArtifactRefs{}, nil, nil, fmt.Errorf("marshal projection artifact: %w", err)
	}
	if err := contractharness.ValidateFlowViewProjection(projectionBytes); err != nil {
		return storage.ArtifactRefs{}, nil, nil, fmt.Errorf("projection artifact contract: %w", err)
	}
	readSetBytes, err := json.Marshal(result.ReadSet)
	if err != nil {
		return storage.ArtifactRefs{}, nil, nil, fmt.Errorf("marshal analysis read-set artifact: %w", err)
	}
	if err := contractharness.Validate(rflscvs02.ReadSetSchemaID, readSetBytes); err != nil {
		return storage.ArtifactRefs{}, nil, nil, fmt.Errorf("analysis read-set artifact contract: %w", err)
	}
	closureBytes, err := json.Marshal(result.Closure)
	if err != nil {
		return storage.ArtifactRefs{}, nil, nil, fmt.Errorf("marshal observation closure artifact: %w", err)
	}
	if err := contractharness.Validate(rflscvs02.ClosureSchemaID, closureBytes); err != nil {
		return storage.ArtifactRefs{}, nil, nil, fmt.Errorf("observation closure artifact contract: %w", err)
	}
	resultBytes, err := json.Marshal(result)
	if err != nil {
		return storage.ArtifactRefs{}, nil, nil, fmt.Errorf("marshal analyzer result artifact: %w", err)
	}
	if err := contractharness.Validate(rflscvs02.AnalyzerResultSchemaID, resultBytes); err != nil {
		return storage.ArtifactRefs{}, nil, nil, fmt.Errorf("analyzer result artifact contract: %w", err)
	}
	artifacts := map[string][]byte{
		storage.ArtifactCASRef(mapBytes):        mapBytes,
		storage.ArtifactCASRef(projectionBytes): projectionBytes,
		storage.ArtifactCASRef(readSetBytes):    readSetBytes,
		storage.ArtifactCASRef(closureBytes):    closureBytes,
		storage.ArtifactCASRef(resultBytes):     resultBytes,
	}
	refs := storage.ArtifactRefs{
		SemanticMap:        storage.ArtifactCASRef(mapBytes),
		Projection:         storage.ArtifactCASRef(projectionBytes),
		AnalysisReadSet:    storage.ArtifactCASRef(readSetBytes),
		ObservationClosure: storage.ArtifactCASRef(closureBytes),
		AnalyzerResult:     storage.ArtifactCASRef(resultBytes),
	}
	if len(semanticDeltas) == 1 && semanticDeltas[0] != nil {
		deltaBytes, deltaErr := json.Marshal(semanticDeltas[0])
		if deltaErr != nil {
			return storage.ArtifactRefs{}, nil, nil, fmt.Errorf("marshal semantic delta artifact: %w", deltaErr)
		}
		if deltaErr := contractharness.ValidateSemanticDeltaIR(deltaBytes); deltaErr != nil {
			return storage.ArtifactRefs{}, nil, nil, fmt.Errorf("semantic delta artifact contract: %w", deltaErr)
		}
		deltaRef := storage.ArtifactCASRef(deltaBytes)
		artifacts[deltaRef] = deltaBytes
		refs.SemanticDelta = deltaRef
	}
	digests := make(map[string]string, len(artifacts))
	for name, ref := range map[string]string{
		"semanticMap":        refs.SemanticMap,
		"projection":         refs.Projection,
		"analysisReadSet":    refs.AnalysisReadSet,
		"observationClosure": refs.ObservationClosure,
		"analyzerResult":     refs.AnalyzerResult,
		"semanticDelta":      refs.SemanticDelta,
	} {
		if ref != "" {
			digests[name] = strings.TrimPrefix(ref, "cas:sha256:")
		}
	}
	return refs, artifacts, digests, nil
}

// semanticDeltaForPublication compares the candidate against the currently
// active proof. The active proof is the only durable predecessor authority,
// so a missing or invalid predecessor fails the publication rather than
// producing an unbound delta artifact.
func semanticDeltaForPublication(st *storage.Storage, previous *storage.ActivePointer, current *semantic.SemanticMapIR) (*semantic.SemanticDeltaIR, error) {
	if previous == nil {
		return nil, nil
	}
	if st == nil || current == nil {
		return nil, fmt.Errorf("publication predecessor and candidate are required")
	}
	if previous.GenerationID == "" {
		return nil, fmt.Errorf("publication predecessor generation identity is missing")
	}
	bundle, err := st.ReadValidatedActiveProofBundle()
	if err != nil {
		return nil, fmt.Errorf("read validated publication predecessor: %w", err)
	}
	if bundle == nil || bundle.Manifest == nil || bundle.Pointer == nil || len(bundle.SemanticMap) == 0 {
		return nil, fmt.Errorf("validated publication predecessor is unavailable")
	}
	if bundle.Manifest.GenerationID != previous.GenerationID || bundle.Pointer.GenerationID != previous.GenerationID {
		return nil, fmt.Errorf("validated publication predecessor does not match active pointer")
	}
	if err := contractharness.ValidateSemanticMapIR(bundle.SemanticMap); err != nil {
		return nil, fmt.Errorf("publication predecessor semantic map contract: %w", err)
	}
	var baseline semantic.SemanticMapIR
	if err := json.Unmarshal(bundle.SemanticMap, &baseline); err != nil {
		return nil, fmt.Errorf("decode publication predecessor semantic map: %w", err)
	}
	if baseline.GenerationID != previous.GenerationID || baseline.ComputedBasisID != bundle.Manifest.ComputedBasisID {
		return nil, fmt.Errorf("publication predecessor semantic map identity does not match active proof")
	}
	if current.GenerationID == baseline.GenerationID {
		return nil, fmt.Errorf("publication candidate reuses predecessor generation")
	}
	comparisonID := "comparison-" + baseline.GenerationID + "-" + current.GenerationID
	delta, err := semantic.ComputeSemanticDelta(comparisonID, &baseline, current)
	if err != nil {
		return nil, fmt.Errorf("compute semantic delta: %w", err)
	}
	return delta, nil
}

func (s *Server) startLiveConsumer() {
	liveCtx, liveCancel := context.WithCancel(context.Background())
	s.liveMu.Lock()
	s.liveCtx, s.liveCancel = liveCtx, liveCancel
	done := make(chan struct{})
	s.liveDone = done
	s.liveWG.Add(1)
	s.liveMu.Unlock()
	go func() {
		defer s.liveWG.Done()
		defer close(done)
		for {
			select {
			case <-liveCtx.Done():
				return
			case snapshot, ok := <-s.scheduler.Checkpoints():
				if !ok {
					return
				}
				analysisCtx, finishAnalysis := s.beginCheckpoint(liveCtx)
				s.processCheckpoint(analysisCtx, snapshot)
				finishAnalysis()
			}
		}
	}()
}

// beginCheckpoint gives one selected checkpoint an owned cancellation scope.
// A later accepted edit cancels this scope, while the scheduler retains the
// newest snapshot for the next checkpoint cycle.
func (s *Server) beginCheckpoint(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	s.analysisMu.Lock()
	previous := s.analysisCancel
	s.analysisCancel = cancel
	s.analysisMu.Unlock()
	if previous != nil {
		previous()
	}
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			s.analysisMu.Lock()
			if s.analysisCancel != nil {
				s.analysisCancel = nil
			}
			s.analysisMu.Unlock()
			cancel()
		})
	}
}

func (s *Server) cancelActiveAnalysis() {
	s.analysisMu.Lock()
	cancel := s.analysisCancel
	s.analysisMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Server) processCheckpoint(ctx context.Context, snap *workspace.WorkspaceSnapshot) (resultErr error) {
	if snap == nil || ctx == nil || ctx.Err() != nil {
		return
	}
	activity := s.engine.CurrentActivity()
	s.engine.BeginAnalysis(snap.SnapshotID, activity.TraceID)
	settled := false
	var gapErr error
	defer func() {
		s.engine.EndAnalysis(snap.SnapshotID, settled)
		if err := s.publishActivity(); err != nil && resultErr == nil {
			resultErr = fmt.Errorf("publish terminal activity: %w", err)
		}
		if gapErr != nil && resultErr == nil {
			resultErr = gapErr
		}
		if resultErr != nil {
			s.recordPipelineError(resultErr)
		}
	}()
	emitGap := func(reason string) {
		if ctx.Err() != nil {
			return
		}
		if err := s.publishGap(snap, reason, activity); err != nil {
			gapErr = err
		} else {
			settled = true
		}
	}

	req := s.latestLiveRequest()
	if req == nil {
		emitGap("no active task query")
		return
	}
	lease, err := s.engine.SnapshotVFS(snap.SnapshotID)
	if err != nil {
		emitGap("snapshot lease unavailable: " + err.Error())
		return
	}
	defer lease.Close()
	protocolSnapshot, err := protocol.SnapshotFromLease(lease)
	if err != nil {
		emitGap("snapshot protocol conversion failed: " + err.Error())
		return
	}
	mapIR, projection, slicePayload, _, intent, closure, err := s.compileSnapshotCandidate(ctx, protocolSnapshot, req.query)
	if err != nil {
		emitGap(err.Error())
		return
	}
	// The compiler emits a historical candidate. Once the complete current
	// publication gate is evaluated and its proof is committed, the exact map
	// artifact becomes the current enrichment basis. Keep this transition in
	// the publication boundary so direct compiler callers retain candidate
	// semantics.
	mapIR.Freshness = "current"
	if ctx.Err() != nil {
		return
	}
	// Capture the live head exactly once for this gate. The same immutable
	// observation is used for delta computation, eligibility, and the commit
	// CAS. A commit-time validator below rejects a head that advances after
	// this observation.
	live := s.engine.LiveHead()
	if live == nil {
		emitGap("live workspace head unavailable")
		return
	}
	delta, err := s.engine.ComputeDelta(snap.SnapshotID, live.SnapshotID)
	if err != nil {
		emitGap("compute workspace delta: " + err.Error())
		return
	}
	if ctx.Err() != nil {
		return
	}
	// Settlement is part of the canonical map bytes. Evaluate and copy it
	// before hashing or staging any artifact so the manifest and map cannot
	// disagree after restart.
	settlement := s.gate.EvaluateSettlement(mapIR)
	mapIR.Settlement = settlement.Gate
	mapBytes, err := json.Marshal(mapIR)
	if err != nil {
		emitGap("marshal candidate: " + err.Error())
		return
	}
	if err := contractharness.ValidateSemanticMapIR(mapBytes); err != nil {
		emitGap("semantic map artifact contract: " + err.Error())
		return
	}
	previous, err := s.storage.ReadActivePointer()
	if err != nil {
		emitGap("read active publication predecessor: " + err.Error())
		return
	}
	semanticDelta, err := semanticDeltaForPublication(s.storage, previous, mapIR)
	if err != nil {
		emitGap(err.Error())
		return
	}
	previousID := ""
	if previous != nil {
		previousID = previous.GenerationID
	}
	if ctx.Err() != nil {
		return
	}
	metrics := s.engine.CurrentActivity()
	var analysisRequest *rflscvs02.AnalyzerRequest
	analysisResult := slicePayload.ValidatedResult
	capabilityProfileDigest := ""
	if analysisResult != nil {
		analysisInput, inputErr := protocolSnapshot.AnalyzerInput()
		if inputErr != nil {
			emitGap("analyzer request snapshot conversion failed: " + inputErr.Error())
			return
		}
		request, requestErr := rflscvs02.NewAnalyzerRequest(analysisResult.RequestID, analysisResult.Operation, analysisInput, nil, analysisResult.Closure.RequiredObservations)
		if requestErr != nil {
			emitGap("analyzer request identity failed: " + requestErr.Error())
			return
		}
		analysisRequest = &request
		capabilityProfileDigest, err = semantic.CanonicalCapabilityProfileDigest(analysisResult.Capability)
		if err != nil {
			emitGap("analyzer capability profile digest failed: " + err.Error())
			return
		}
	}
	artifactRefs, artifactBytes, artifactDigests, artifactErr := canonicalPublicationArtifacts(mapBytes, projection, analysisResult, semanticDelta)
	if artifactErr != nil {
		emitGap("canonical publication artifacts: " + artifactErr.Error())
		return
	}
	gateResult, gap := s.gate.EvaluateCurrent(semantic.PublicationInput{
		Map: mapIR, Closure: closure, Delta: delta, CapturedSnapshot: snap, LiveHeadSnapshot: live, Intent: intent,
		RepositoryID: snap.RepositoryID, WorktreeID: snap.WorktreeID, DependencyFingerprint: snap.DependencyFingerprint, QueryHash: queryIdentity(req.query), GenerationID: mapIR.GenerationID, ExpectedPreviousGenerationID: previousID,
		AnalysisRequest:         analysisRequest,
		AnalysisResult:          analysisResult,
		CapabilityProfileDigest: capabilityProfileDigest,
		ArtifactDigests:         artifactDigests, Metrics: semantic.PublicationMetrics{LagMs: metrics.AnalysisLagMs, PendingRevisions: metrics.PendingRevisions, MeasuredAt: metrics.Timestamp, Activity: metrics.Activity, TraceID: metrics.TraceID},
	})
	if gateResult.Eligibility != "passed" || gap != nil {
		if ctx.Err() != nil {
			return
		}
		if gap == nil {
			gap = &semantic.VerifiedGap{SchemaID: "https://codeflow.local/schemas/rflsc.verified-gap.v2.schema.json", SchemaVersion: 2, Freshness: "last_verified", Activity: metrics.Activity, LatestSnapshotID: snap.SnapshotID, WorkspaceEpoch: snap.WorkspaceEpoch, AffectedScope: []string{}, AnalysisLagMs: metrics.AnalysisLagMs, PendingRevisions: metrics.PendingRevisions, IntersectedCauses: []string{"publication gate rejected current"}, Timestamp: metrics.Timestamp, TraceID: metrics.TraceID}
		}
		if err := s.publishGapValue(gap, snap); err != nil {
			gapErr = err
		} else {
			settled = true
		}
		return
	}
	now := metrics.Timestamp
	if now.IsZero() {
		now = snap.CreatedAt
	}
	manifest := &storage.GenerationProofManifest{SchemaID: semantic.GenerationProofSchemaID, SchemaVersion: semantic.SemanticSchemaVersion, ProofID: "proof-" + mapIR.GenerationID, GenerationID: mapIR.GenerationID, ComputedBasisID: mapIR.ComputedBasisID, ComputedSnapshotID: snap.SnapshotID, ValidatedAgainstSnapshotID: live.SnapshotID, TaskIntentRevision: intent.Revision, NormalizedQueryHash: queryIdentity(req.query), AnalysisReadSetID: closure.AnalysisReadSetID, CausalObservationClosureID: closure.ClosureID, CausalObservationClosureDigest: closure.ClosureDigest, CapabilityProfileDigest: capabilityProfileDigest, WorkspaceEpoch: snap.WorkspaceEpoch, CurrentPublication: storage.CurrentPublicationResult{Eligibility: gateResult.Eligibility, SnapshotGate: gateResult.SnapshotGate, ClosureGate: gateResult.ClosureGate, EvidenceGate: gateResult.EvidenceGate, SemanticAtomicityGate: gateResult.SemanticAtomicityGate, TaskRelevanceGate: gateResult.TaskRelevanceGate, ComprehensionGate: gateResult.ComprehensionGate}, SettlementEvaluation: storage.SettlementEvaluation{Gate: settlement.Gate, EvaluatedAt: settlement.EvaluatedAt, BlockingObligationRefs: append([]string{}, settlement.BlockingObligationRefs...)}, ArtifactRefs: artifactRefs, ExpectedLiveHeadSnapshotID: live.SnapshotID, PublishedAt: now}
	if previousID != "" {
		manifest.ExpectedPreviousGenerationID = &previousID
	}
	pointer := &storage.ActivePointer{SchemaID: semantic.ActivePointerSchemaID, SchemaVersion: semantic.SemanticSchemaVersion, GenerationID: mapIR.GenerationID, ComputedBasisID: mapIR.ComputedBasisID, ValidatedAgainstSnapshotID: live.SnapshotID, ExpectedLiveHeadSnapshotID: live.SnapshotID, WorkspaceEpoch: snap.WorkspaceEpoch, TaskIntentRevision: intent.Revision, NormalizedQueryHash: queryIdentity(req.query), FlowCount: 1, RepositoryID: snap.RepositoryID, WorktreeID: snap.WorktreeID, TaskID: intent.TaskID, PublishedAt: now}
	if previousID != "" {
		pointer.ExpectedPreviousGenerationID = &previousID
	}
	data := map[string]any{"manifest": manifest, "projection": projection, "slice": slicePayload}
	genID := mapIR.GenerationID
	_, err = s.hub.PublishAtomic("generation.published", data, &mapIR.ComputedBasisID, &snap.SnapshotID, &genID, func(env *semantic.EventEnvelope, eventBytes []byte) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, txErr := s.storage.PublishGeneration(storage.PublicationTransaction{Manifest: manifest, Pointer: pointer, Event: eventBytes, Artifacts: artifactBytes, ExpectedLiveHeadSnapshotID: live.SnapshotID, ActualLiveHeadSnapshotID: live.SnapshotID, LiveHeadCommit: func(expectedID string, commit func() error) error { return s.engine.WithLiveHead(expectedID, commit) }, ExpectedPreviousGenerationID: previousID})
		return txErr
	})
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if gapErr = s.publishGap(snap, "publication transaction failed: "+err.Error(), metrics); gapErr == nil {
			settled = true
		}
		return
	}
	settled = true
	mapIR.Authority = "candidate" // current authority is represented by the proof manifest, not model text
	s.mu.Lock()
	s.mapCache[mapIR.GenerationID] = mapIR
	s.mapCache[mapIR.ComputedBasisID] = mapIR
	s.mu.Unlock()
	return nil
}

func (s *Server) publishActivity() error {
	act := s.engine.CurrentActivity()
	activityBytes, err := json.Marshal(act)
	if err != nil {
		return fmt.Errorf("marshal activity: %w", err)
	}
	if err := contractharness.ValidateActivityStateV2(activityBytes); err != nil {
		return fmt.Errorf("activity-state contract: %w", err)
	}
	_, err = s.hub.PublishChecked("activity.updated", act, nil, &act.CurrentSnapshotID, nil)
	return err
}

func (s *Server) publishGap(snap *workspace.WorkspaceSnapshot, reason string, activity workspace.ActivityStatus) error {
	if snap == nil {
		return fmt.Errorf("snapshot is nil")
	}
	if activity.TraceID == "" || activity.Timestamp.IsZero() || activity.AnalysisLagMs < 0 || activity.PendingRevisions < 0 {
		return fmt.Errorf("gap metrics are unmeasured")
	}
	gap := &semantic.VerifiedGap{SchemaID: "https://codeflow.local/schemas/rflsc.verified-gap.v2.schema.json", SchemaVersion: 2, Freshness: "last_verified", Activity: activity.Activity, LatestSnapshotID: snap.SnapshotID, WorkspaceEpoch: snap.WorkspaceEpoch, AffectedScope: []string{}, AnalysisLagMs: activity.AnalysisLagMs, PendingRevisions: activity.PendingRevisions, IntersectedCauses: []string{reason}, Timestamp: activity.Timestamp, TraceID: activity.TraceID}
	return s.publishGapValue(gap, snap)
}

func (s *Server) publishGapValue(gap *semantic.VerifiedGap, snap *workspace.WorkspaceSnapshot) error {
	if gap == nil || snap == nil {
		return fmt.Errorf("gap and snapshot are required")
	}
	if gap.AffectedScope == nil {
		gap.AffectedScope = []string{}
	}
	if gap.IntersectedCauses == nil {
		gap.IntersectedCauses = []string{"unmeasured publication gap"}
	}
	if gap.SchemaID != "https://codeflow.local/schemas/rflsc.verified-gap.v2.schema.json" || gap.SchemaVersion != 2 || gap.WorkspaceEpoch != snap.WorkspaceEpoch || gap.TraceID == "" || gap.Timestamp.IsZero() || gap.AnalysisLagMs < 0 || gap.PendingRevisions < 0 {
		return fmt.Errorf("gap is not a measured canonical v2 result")
	}
	gapBytes, err := json.Marshal(gap)
	if err != nil {
		return fmt.Errorf("marshal gap: %w", err)
	}
	if err := contractharness.ValidateVerifiedGapV2(gapBytes); err != nil {
		return fmt.Errorf("verified-gap contract: %w", err)
	}
	basis := snap.ComputedBasisID
	snapshotID := snap.SnapshotID
	_, err = s.hub.PublishChecked("generation.gap", gap, &basis, &snapshotID, nil)
	return err
}
