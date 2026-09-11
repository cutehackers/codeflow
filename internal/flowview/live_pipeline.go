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
	"codeflow/internal/fusion"
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

type persistedLiveView struct {
	SchemaID        string                            `json:"schemaId"`
	SchemaVersion   int                               `json:"schemaVersion"`
	GenerationID    string                            `json:"generationId"`
	ComputedBasisID string                            `json:"computedBasisId"`
	SnapshotID      string                            `json:"snapshotId"`
	FlowContexts    map[string]*FlowContextProjection `json:"flowContexts"`
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

// liveAdapterPool returns the persistent adapter pool for lang, creating
// and registering it once per server lifecycle instead of spawning fresh
// adapter processes on every checkpoint.
func (s *Server) liveAdapterPool(lang string, cfg protocol.Config) (*protocol.Pool, error) {
	s.adapterRegistryMu.Lock()
	defer s.adapterRegistryMu.Unlock()
	if s.adapterRegistry == nil {
		s.adapterRegistry = protocol.NewAdapterRegistry(2)
	}
	s.adapterRegistry.RegisterConfig(lang, cfg)
	return s.adapterRegistry.GetPool(lang)
}

func (s *Server) closeAdapterRegistry() {
	s.adapterRegistryMu.Lock()
	defer s.adapterRegistryMu.Unlock()
	if s.adapterRegistry != nil {
		s.adapterRegistry.Close()
		s.adapterRegistry = nil
	}
}

// lastPublishedEntry returns the entry symbol path of the last committed
// live publication, or "" when none was published yet.
func (s *Server) lastPublishedEntry() string {
	s.projectMu.RLock()
	defer s.projectMu.RUnlock()
	return s.lastPublishedEntrySymbolPath
}

// selectProjectChangeCandidate picks the recompile target for a workspace
// edit. Entry-path matches win first. Otherwise an edit intersecting the
// active proof bundle read set re-slices the active flow (or candidates[0]
// when the previous entry is gone). Any other in-language source edit falls
// back to candidates[0] instead of dropping the edit as out of scope.
func selectProjectChangeCandidate(candidates []harvest.Candidate, changed map[string]struct{}, activeReadSet map[string]struct{}, lastEntry, lang string) (harvest.Candidate, int) {
	var best harvest.Candidate
	bestScore := -1
	for _, cand := range candidates {
		score := 0
		entryPath := filepath.ToSlash(cand.EntrySymbolPath)
		for changedPath := range changed {
			if changedPath == "" {
				continue
			}
			if strings.Contains(entryPath, changedPath) || strings.Contains(changedPath, entryPath) {
				score += 10
			}
		}
		if score > bestScore {
			bestScore = score
			best = cand
		}
	}
	if bestScore <= 0 && len(activeReadSet) > 0 {
		intersects := false
		for changedPath := range changed {
			if _, ok := activeReadSet[changedPath]; ok {
				intersects = true
				break
			}
		}
		if intersects && len(candidates) > 0 {
			best = candidates[0]
			if lastEntry != "" {
				for _, cand := range candidates {
					if cand.EntrySymbolPath == lastEntry {
						best = cand
						break
					}
				}
			}
			bestScore = 1
		}
	}
	if bestScore <= 0 {
		isSourceChange := false
		for changedPath := range changed {
			ext := strings.ToLower(filepath.Ext(changedPath))
			switch lang {
			case "go":
				if ext == ".go" || changedPath == "go.mod" || changedPath == "go.work" {
					isSourceChange = true
				}
			case "dart":
				if ext == ".dart" || changedPath == "pubspec.yaml" {
					isSourceChange = true
				}
			case "typescript", "javascript":
				if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" || changedPath == "package.json" {
					isSourceChange = true
				}
			default:
				if ext != "" && ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".gif" && ext != ".md" && ext != ".txt" {
					isSourceChange = true
				}
			}
			if isSourceChange {
				break
			}
		}
		if isSourceChange && len(candidates) > 0 {
			best = candidates[0]
			bestScore = 1
		}
	}
	return best, bestScore
}

// activeBundleReadSetPaths returns the document paths of the validated
// active proof bundle read set, or nil when no bundle is validated.
func (s *Server) activeBundleReadSetPaths() map[string]struct{} {
	bundle, err := s.storage.ReadValidatedActiveProofBundle()
	if err != nil || bundle == nil || len(bundle.AnalyzerResult) == 0 {
		return nil
	}
	var decoded struct {
		AnalysisReadSet struct {
			Documents []struct {
				Path string `json:"path"`
			} `json:"documents"`
		} `json:"analysisReadSet"`
	}
	if err := json.Unmarshal(bundle.AnalyzerResult, &decoded); err != nil {
		return nil
	}
	if len(decoded.AnalysisReadSet.Documents) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(decoded.AnalysisReadSet.Documents))
	for _, doc := range decoded.AnalysisReadSet.Documents {
		if doc.Path != "" {
			out[filepath.ToSlash(doc.Path)] = struct{}{}
		}
	}
	return out
}

// compileSnapshotCandidate is the same adapter/snapshot/compiler path used by
// the background checkpoint consumer. It accepts only the immutable protocol
// snapshot and never falls back to repository reads.
func (s *Server) compileSnapshotCandidate(ctx context.Context, snapshot protocol.Snapshot, query *semantic.TaskViewQuery) (*semantic.SemanticMapIR, *semantic.FlowViewProjection, *slicing.SlicedPayload, *semantic.ResolvedTarget, *semantic.TaskIntent, *semantic.CausalObservationClosure, error) {
	if s.compileCandidate != nil {
		return s.compileCandidate(ctx, snapshot, query)
	}
	if query == nil || (query.Mode != "project_change" && query.Feature == nil) {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("missing_precondition: feature query is required")
	}
	det := detect.DetectSnapshot(snapshot.Files)
	lang := det.Language
	if lang == "" || lang == "unknown" || !det.Confident {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("unsupported_project: live view needs a recognized project marker (pubspec.yaml, package.json, go.mod 등)가 스냅샷에 없습니다")
	}
	cwd, _ := filepath.Abs(".")
	adapterCfg, err := harvest.ResolveAdapterForRepo(s.repoRoot, cwd, lang, "")
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("resolve adapter: %w", err)
	}
	pool, err := s.liveAdapterPool(lang, adapterCfg)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("live adapter pool: %w", err)
	}
	harvester := harvest.NewRunnerWithPool(pool)
	candidates, err := harvester.RunWithSnapshot(ctx, s.repoRoot, snapshot)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("harvest candidates: %w", err)
	}
	var resolved *semantic.ResolvedTarget
	if query.Mode == "project_change" {
		if len(candidates) > 0 {
			changed := map[string]struct{}{}
			if wsSnap, wsErr := s.engine.GetSnapshot(snapshot.SnapshotID); wsErr == nil && wsSnap != nil {
				for _, entry := range wsSnap.ChangedEntries {
					if entry.Path != "" {
						changed[filepath.ToSlash(entry.Path)] = struct{}{}
					}
				}
			}
			if len(changed) == 0 {
				for _, doc := range snapshot.Documents {
					if doc.Path != "" {
						changed[filepath.ToSlash(doc.Path)] = struct{}{}
					}
				}
			}
			best, bestScore := selectProjectChangeCandidate(candidates, changed, s.activeBundleReadSetPaths(), s.lastPublishedEntry(), lang)
			if len(changed) > 0 && bestScore <= 0 {
				return nil, nil, nil, nil, nil, nil, fmt.Errorf("이 변경은 현재 분석 범위에서 확인할 수 없습니다")
			}
			if len(candidates) == 0 {
				return nil, nil, nil, nil, nil, nil, fmt.Errorf("이 변경은 현재 분석 범위에서 확인할 수 없습니다")
			}
			c := best
			resolved = &semantic.ResolvedTarget{
				EntrySymbolPath: c.EntrySymbolPath,
				CandidateID:     c.CandidateID,
				FlowID:          fusion.ComputeFlowID(c.EntrySymbolPath),
				Title:           "프로젝트 변경 감시",
			}
		} else {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("이 변경은 현재 분석 범위에서 확인할 수 없습니다")
		}
	} else {
		resolved, err = semantic.ResolveFeatureQueryTarget(query, candidates)
		if err != nil {
			return nil, nil, nil, nil, nil, nil, err
		}
	}
	slicePayload, err := slicing.NewRunner(pool).SliceWithSnapshot(ctx, s.repoRoot, resolved.CandidateID, resolved.EntrySymbolPath, nil, snapshot)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("slice: %w", err)
	}
	requestText := "프로젝트 변경 감시"
	if query.Feature != nil && strings.TrimSpace(query.Feature.Request) != "" {
		requestText = strings.TrimSpace(query.Feature.Request)
	} else if resolved.Title != "" {
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
	capability := false
	if conn, err := pool.Get(ctx); err == nil {
		capability = conn.Version().Capabilities.FlowContext
		pool.Put(conn)
	}
	s.rememberFlowContexts(mapIR, slicePayload, capability)
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
		scopeRef := observation.Path
		if rflscvs02.IsZeroMissMarker(observation) {
			scopeRef = ""
		}
		closure.NegativeObservations = append(closure.NegativeObservations, semantic.NegativeObservation{Kind: observation.Kind, Selector: observation.Path, ScopeRef: scopeRef, ObservedAgainstIndexRevision: observation.ValueHash})
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

func (s *Server) liveViewArtifact(mapIR *semantic.SemanticMapIR, snapshot protocol.Snapshot) ([]byte, error) {
	if mapIR == nil || mapIR.GenerationID == "" || mapIR.ComputedBasisID == "" || snapshot.SnapshotID == "" {
		return nil, fmt.Errorf("Live view artifact identity is incomplete")
	}
	s.mu.Lock()
	metadata := s.flowContextMetadata[mapIR.GenerationID]
	s.mu.Unlock()
	contexts := make(map[string]*FlowContextProjection, len(mapIR.Steps))
	files := snapshotSourceBytes(snapshot)
	for _, step := range mapIR.Steps {
		contexts[step.StepID] = DeriveFlowContext(DeriveFlowContextParams{
			Step: step, SemanticMap: mapIR, SnapshotFiles: files, SourceSnapshotID: snapshot.SnapshotID,
			AdapterHasFlowContext: metadata.Capability, Metadata: metadata.Steps[step.StepID], Expansion: ExpansionFlowContext,
		})
	}
	return json.Marshal(persistedLiveView{
		SchemaID: "https://codeflow.local/schemas/rflsc.live-generation-view.v1.schema.json", SchemaVersion: 1,
		GenerationID: mapIR.GenerationID, ComputedBasisID: mapIR.ComputedBasisID, SnapshotID: snapshot.SnapshotID, FlowContexts: contexts,
	})
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
		watcherDone := make(chan struct{})
		go func() {
			defer close(watcherDone)
			s.runWorkspaceWatcher(liveCtx)
		}()
		defer func() { <-watcherDone }()
		s.projectMu.RLock()
		isProjectChange := s.projectMode == "project_change"
		s.projectMu.RUnlock()
		if isProjectChange {
			if bundle, bErr := s.storage.ReadValidatedActiveProofBundle(); bErr != nil || bundle == nil || bundle.Manifest == nil {
				needsBaseline := s.scheduler == nil || !s.scheduler.HasPending()
				if needsBaseline {
					s.projectMu.Lock()
					s.baselineCompileQueued = true
					s.projectMu.Unlock()
					if head := s.engine.LiveHead(); head != nil && s.scheduler != nil {
						s.scheduler.NotifyEdit(head)
					}
				}
			}
		}
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
	if req == nil && s.projectMode == "project_change" {
		req = &liveRequest{
			query: &semantic.TaskViewQuery{
				Mode: "project_change",
				Feature: &semantic.FeatureQueryParams{
					Request: "프로젝트 변경 감시",
				},
			},
			requestText: "프로젝트 변경 감시",
		}
	}
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
	mapIR, projection, slicePayload, resolved, intent, closure, err := s.compileSnapshotCandidate(ctx, protocolSnapshot, req.query)
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
	liveViewBytes, liveViewErr := s.liveViewArtifact(mapIR, protocolSnapshot)
	if liveViewErr != nil {
		emitGap("Live view artifact: " + liveViewErr.Error())
		return
	}
	liveViewRef := storage.ArtifactCASRef(liveViewBytes)
	artifactRefs.LiveView = liveViewRef
	artifactBytes[liveViewRef] = liveViewBytes
	artifactDigests["liveView"] = strings.TrimPrefix(liveViewRef, "cas:sha256:")
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
	_, err = s.hub.PublishAtomic("generation.published", data, &mapIR.ComputedBasisID, &live.SnapshotID, &genID, func(env *semantic.EventEnvelope, eventBytes []byte) error {
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

	s.projectMu.Lock()
	s.lastVerifiedBasisID = live.SnapshotID
	if resolved != nil && resolved.EntrySymbolPath != "" {
		s.lastPublishedEntrySymbolPath = resolved.EntrySymbolPath
	}
	s.projectStatus = "watching"
	s.projectNotice = "현재 프로젝트의 변경을 감시하고 있습니다"
	s.projectGap = nil
	_ = WriteAnalysisBasis(s.repoRoot, AnalysisBasisRecord{
		BasisSnapshotID: live.SnapshotID,
		WorkspaceEpoch:  live.WorkspaceEpoch,
		RootTreeID:      live.RootTreeID,
	})
	s.projectMu.Unlock()
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
	s.projectMu.Lock()
	s.projectStatus = "gap"
	s.projectGap = gap
	notice := "최신 변경을 확인하지 못했습니다. 이전 코드 표시 중"
	for _, cause := range gap.IntersectedCauses {
		lower := strings.ToLower(cause)
		if strings.Contains(lower, "scope") || strings.Contains(cause, "범위") {
			notice = "이 변경은 현재 분석 범위에서 확인할 수 없습니다"
			break
		}
		if strings.Contains(cause, "새 변경을 확인하지 못했습니다") || strings.Contains(lower, "conflict") {
			notice = "새 변경을 확인하지 못했습니다"
			break
		}
	}
	s.projectNotice = notice
	s.projectMu.Unlock()
	basis := snap.ComputedBasisID
	snapshotID := snap.SnapshotID
	_, err = s.hub.PublishChecked("generation.gap", gap, &basis, &snapshotID, nil)
	return err
}
