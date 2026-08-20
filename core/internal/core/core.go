// Package core starts the fixture-backed CodeFlow walking skeleton.
package core

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	compare "codeflow/core/internal/comparison"
	"codeflow/core/internal/compiler"
	"codeflow/core/internal/delta"
	"codeflow/core/internal/entrypoint"
	"codeflow/core/internal/flowir"
	"codeflow/core/internal/lens"
	"codeflow/core/internal/manifest"
	"codeflow/core/internal/ontology"
	"codeflow/core/internal/runtime"
	"codeflow/core/internal/store"
	"codeflow/core/internal/workspacewatch"
)

type Core struct {
	URL, Token      string
	server          *http.Server
	listener        net.Listener
	runtime         runtime.Handle
	store           *store.Store
	repo            string
	adapterCommand  string
	flowID          string
	capture         func(string) (flowir.Basis, error)
	analysis        *AnalysisOptions
	analysisSession *compiler.Session
	reconcileMu     sync.Mutex
	reconcileTimer  *time.Timer
	reconcileWG     sync.WaitGroup
	closed          bool
	watcher         *workspacewatch.Watcher
	comparison      *compare.Result
	overlayMu       sync.Mutex
	overlays        []ontology.Candidate
	domainMu        sync.Mutex
}

// envelope is the common CodeFlowResponse contract. Runtime-only publication
// data is separate from deterministic FlowIR but appears consistently here.
type envelope struct {
	Basis    *flowir.Basis      `json:"basis"`
	Status   string             `json:"status"`
	Data     any                `json:"data"`
	Unknowns []any              `json:"unknowns"`
	Reviews  []store.DebtReview `json:"debt_reviews,omitempty"`
	Lenses   []lens.Source      `json:"lenses,omitempty"`
	ViewURL  string             `json:"view_url"`
	Metadata metadata           `json:"metadata,omitempty"`
	Error    *apiError          `json:"error,omitempty"`
}
type metadata struct {
	GeneratedAt   string `json:"generated_at"`
	RuntimeStatus string `json:"runtime_status"`
	ViewURL       string `json:"view_url,omitempty"`
}
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type workspaceDocument struct {
	SchemaVersion string              `json:"schema_version"`
	Basis         flowir.Basis        `json:"basis"`
	FlowIDs       []string            `json:"flow_ids"`
	Flows         []flowir.Document   `json:"flows"`
	Edges         []workspaceFlowEdge `json:"screen_flow_edges"`
}

// workspaceDocumentV2 is the compact read model for UI and agent navigation.
// Canonical FlowIR remains available from the per-flow v1 endpoint; this model
// shares snapshot identity once and never repeats the repository manifest in
// every flow document.
type workspaceDocumentV2 struct {
	SchemaVersion string                    `json:"schema_version"`
	FlowIDs       []string                  `json:"flow_ids"`
	Flows         []workspaceFlowDocumentV2 `json:"flows"`
	Edges         []workspaceFlowEdge       `json:"screen_flow_edges"`
}

type workspaceFlowDocumentV2 struct {
	SchemaVersion string                   `json:"schema_version"`
	Facts         []flowir.Fact            `json:"facts"`
	CausalEdges   []flowir.CausalEdge      `json:"causal_edges,omitempty"`
	Architecture  flowir.ArchitectureSlice `json:"architecture"`
	Current       flowir.Flow              `json:"current"`
	Scenarios     []flowir.Scenario        `json:"scenarios,omitempty"`
	Unknowns      []flowir.UnknownDetail   `json:"unknowns,omitempty"`
}

type workspaceBasisV2 struct {
	Repository          string `json:"repository"`
	HeadRevision        string `json:"head_revision"`
	BaselineRevision    string `json:"baseline_revision,omitempty"`
	WorktreeFingerprint string `json:"worktree_fingerprint"`
	Dirty               bool   `json:"dirty"`
	ManifestCount       int    `json:"manifest_count"`
}

type workspaceEnvelopeV2 struct {
	Basis    workspaceBasisV2 `json:"basis"`
	Status   string           `json:"status"`
	Data     any              `json:"data"`
	Unknowns []any            `json:"unknowns"`
	ViewURL  string           `json:"view_url"`
	Metadata metadata         `json:"metadata,omitempty"`
	Error    *apiError        `json:"error,omitempty"`
}

type workspaceFlowEdge struct {
	ID       string          `json:"id"`
	From     string          `json:"from"`
	To       string          `json:"to"`
	Status   flowir.Status   `json:"status"`
	Evidence []flowir.Anchor `json:"evidence"`
}

type workspaceNavigation struct {
	Flows []workspaceNavigationFlow
	Edges []workspaceFlowEdge
}

type workspaceNavigationFlow struct {
	ID, URL  string
	Status   flowir.Status
	Steps    int
	Unknowns int
	Selected bool
}

type scenarioNavigation struct {
	Flows    []scenarioNavigationItem
	Selected *scenarioNavigationItem
}

type scenarioNavigationItem struct {
	ID, URL, Title string
	Status         flowir.Status
	Steps          int
	Unknowns       int
	Selected       bool
}

type flowViewModel struct {
	Document         flowir.Document
	Status           string
	Resolved         entrypoint.Result
	Comparison       *compare.Result
	Overlays         []ontology.Candidate
	Lenses           map[string]lens.Source
	FactLabels       map[string]string
	Timeline         []timelineItem
	Architecture     []architecturePathItem
	ArchitectureFlow architectureFlow
	Debt             []debtItem
	ResolvedDebt     []store.DebtReview
	Workspace        workspaceNavigation
	Scenarios        scenarioNavigation
	Publication      string
	Export           bool
}

func StartFixture(ctx context.Context, repo string) (*Core, error) {
	return startFixture(ctx, repo, "")
}

// StartFixtureWithAdapter keeps the fixture FlowIR while making the resolver
// public path use an explicitly owned adapter command. Production callers
// should pass the installed adapter path, never a target repository path.
func StartFixtureWithAdapter(ctx context.Context, repo, adapterCommand string) (*Core, error) {
	return startFixture(ctx, repo, adapterCommand)
}
func startFixture(ctx context.Context, repo, adapterCommand string) (*Core, error) {
	lock, err := runtime.Acquire(repo)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Core, error) { lock.Release(); return nil, err }
	database, err := store.Open(lock.Dir)
	if err != nil {
		return fail(err)
	}
	basis, err := manifest.Capture(repo)
	if err != nil {
		database.Close()
		return fail(err)
	}
	document, err := flowir.Fixture(basis.Repository, basis)
	if err != nil {
		database.Close()
		return fail(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err = database.Publish(ctx, document, now, "ready"); err != nil {
		database.Close()
		return fail(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		database.Close()
		return fail(err)
	}
	token, err := runtime.Token()
	if err != nil {
		listener.Close()
		database.Close()
		return fail(err)
	}
	instance := &Core{URL: "http://" + listener.Addr().String(), Token: token, listener: listener, runtime: lock, store: database, repo: basis.Repository, adapterCommand: adapterCommand, flowID: document.Current.ID, capture: manifest.Capture}
	if err = lock.Write(listener.Addr().(*net.TCPAddr).Port, token, now); err != nil {
		listener.Close()
		database.Close()
		lock.Release()
		return nil, err
	}
	instance.server = localHTTPServer(instance.routes())
	go instance.server.Serve(listener)
	return instance, nil
}

// AnalysisOptions describes the real CF-G05 public path. Unlike StartFixture,
// it accepts only an observed, current graph-and-Dart compiled document.
type AnalysisOptions struct {
	Selector, CodeGraphURL, AdapterCommand string
	Selectors                              []string
}

func StartAnalysis(ctx context.Context, repo string, options AnalysisOptions) (*Core, *compiler.Problem, error) {
	// Establish repository ownership before paying the full analyzer cost. A
	// competing command now learns that a compatible Core already owns this
	// repository immediately instead of compiling for seconds and failing late.
	lock, err := runtime.Acquire(repo)
	if err != nil {
		return nil, nil, err
	}
	selectors := analysisSelectors(options)
	compilerOptions := compiler.Options{Repo: repo, CodeGraphURL: options.CodeGraphURL, AdapterCommand: options.AdapterCommand}
	session, problem, err := compiler.NewSession(ctx, compilerOptions)
	if err != nil || problem != nil {
		lock.Release()
		return nil, problem, err
	}
	documents, problem, err := session.CompileMany(ctx, selectors, nil)
	if err != nil || problem != nil {
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = session.Close(shutdown)
		cancel()
		lock.Release()
		return nil, problem, err
	}
	fail := func(e error) (*Core, *compiler.Problem, error) {
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = session.Close(shutdown)
		cancel()
		lock.Release()
		return nil, nil, e
	}
	database, err := store.Open(lock.Dir)
	if err != nil {
		return fail(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		database.Close()
		return fail(err)
	}
	token, err := runtime.Token()
	if err != nil {
		listener.Close()
		database.Close()
		return fail(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	owned := options
	owned.Selectors = selectors
	owned.Selector = selectors[0]
	flowIDs := make([]string, len(documents))
	for i := range documents {
		flowIDs[i] = documents[i].Current.ID
	}
	instance := &Core{URL: "http://" + listener.Addr().String(), Token: token, listener: listener, runtime: lock, store: database, repo: documents[0].Basis.Repository, adapterCommand: options.AdapterCommand, flowID: flowIDs[0], capture: manifest.Capture, analysis: &owned, analysisSession: session}
	if err = database.PublishBatch(ctx, documents, now, "ready"); err != nil {
		listener.Close()
		database.Close()
		return fail(err)
	}
	if err = lock.Write(listener.Addr().(*net.TCPAddr).Port, token, now); err != nil {
		listener.Close()
		database.Close()
		return fail(err)
	}
	watcher, err := workspacewatch.Start(instance.repo, instance.ScheduleReconcile)
	if err != nil {
		listener.Close()
		database.Close()
		return fail(err)
	}
	instance.watcher = watcher
	instance.server = localHTTPServer(instance.routes())
	go instance.server.Serve(listener)
	return instance, nil, nil
}

func analysisSelectors(options AnalysisOptions) []string {
	if len(options.Selectors) > 0 {
		return append([]string(nil), options.Selectors...)
	}
	return []string{options.Selector}
}

// StartComparison reuses the one current repository Core after compiling the
// selected immutable Git mirror through the same evidence gate.
func StartComparison(ctx context.Context, repo, revision string, options AnalysisOptions) (*Core, *compiler.Problem, error) {
	result, problem, err := compare.Build(ctx, compare.Options{Repo: repo, Revision: revision, Selector: options.Selector, CodeGraphURL: options.CodeGraphURL, AdapterCommand: options.AdapterCommand})
	if err != nil || problem != nil {
		return nil, problem, err
	}
	instance, problem, err := StartAnalysis(ctx, repo, options)
	if err != nil || problem != nil {
		return instance, problem, err
	}
	instance.comparison = &result
	return instance, nil, nil
}

// Reconcile replaces the snapshot only after a complete, verified manifest
// capture. Continued mutation keeps the last consistent snapshot, visibly
// marked as analyzing, rather than publishing a mixture of observations.
func (c *Core) Reconcile(ctx context.Context) error {
	c.reconcileMu.Lock()
	defer c.reconcileMu.Unlock()
	if c.closed {
		return nil
	}
	if c.analysis != nil {
		return c.reconcileAnalysis(ctx)
	}
	basis, err := c.capture(c.repo)
	if err != nil {
		if errors.Is(err, manifest.ErrChanging) {
			return c.store.SetStatus(ctx, c.flowID, "analyzing")
		}
		return err
	}
	document, err := flowir.Fixture(basis.Repository, basis)
	if err != nil {
		return err
	}
	return c.store.Publish(ctx, document, time.Now().UTC().Format(time.RFC3339Nano), "ready")
}

// ScheduleReconcile accepts only a change notification. It persists neither
// the event nor any meaning inferred from it; the later reconciliation owns
// all source observation and publication decisions.
func (c *Core) ScheduleReconcile() {
	c.reconcileMu.Lock()
	defer c.reconcileMu.Unlock()
	if c.closed {
		return
	}
	if c.reconcileTimer != nil && c.reconcileTimer.Stop() {
		c.reconcileWG.Done()
	}
	c.reconcileWG.Add(1)
	c.reconcileTimer = time.AfterFunc(100*time.Millisecond, func() {
		defer c.reconcileWG.Done()
		_ = c.Reconcile(context.Background())
	})
}

// Refresh is the public missed-event recovery path. It deliberately executes
// the same authoritative reconciliation as a scheduled notification.
func (c *Core) Refresh(ctx context.Context) error { return c.Reconcile(ctx) }

func (c *Core) reconcileAnalysis(ctx context.Context) error {
	previous, _, _, err := c.store.GetBatch(ctx)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 2; attempt++ {
		basis, err := c.capture(c.repo)
		if err != nil {
			if errors.Is(err, manifest.ErrChanging) {
				return c.store.SetStatus(ctx, c.flowID, "analyzing")
			}
			return err
		}
		if len(previous) > 0 && flowir.SameBasis(previous[0].Basis, basis) {
			return c.store.SetBatchStatus(ctx, "ready")
		}
		// Resolved analysis may take seconds. Expose that work while retaining
		// the complete prior snapshot, never a mixture of old and new facts.
		if err := c.store.SetBatchStatus(ctx, "analyzing"); err != nil {
			return err
		}
		affected := affectedFlowIDs(previous, basis)
		compiled := []flowir.Document{}
		var problem *compiler.Problem
		if len(affected) > 0 {
			compiled, problem, err = c.compileAnalysis(ctx, basis, affected)
		}
		if err != nil || problem != nil {
			_ = c.store.SetBatchStatus(ctx, "analyzing")
			if err != nil {
				return err
			}
			return nil
		}
		documents, mergeErr := mergeReconciled(previous, compiled, basis)
		if mergeErr != nil {
			return mergeErr
		}
		verified, err := c.capture(c.repo)
		if err == nil && verified.WorktreeFingerprint == basis.WorktreeFingerprint && verified.HeadRevision == basis.HeadRevision {
			return c.store.PublishBatch(ctx, documents, time.Now().UTC().Format(time.RFC3339Nano), "ready")
		}
	}
	return c.store.SetBatchStatus(ctx, "analyzing")
}

func (c *Core) compileAnalysis(ctx context.Context, basis flowir.Basis, selectors []string) ([]flowir.Document, *compiler.Problem, error) {
	documents, problem, err := c.analysisSession.CompileMany(ctx, selectors, &basis)
	if problem == nil || !strings.HasPrefix(problem.Code, "ADAPTER_") {
		return documents, problem, err
	}
	// Cancellation or a malformed child response permanently closes the
	// protocol stream. Replace the owned process once so a later edit or manual
	// refresh can recover without restarting CodeFlow itself.
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	_ = c.analysisSession.Close(shutdown)
	cancel()
	replacement, replacementProblem, replacementErr := compiler.NewSession(ctx, compiler.Options{Repo: c.repo, CodeGraphURL: c.analysis.CodeGraphURL, AdapterCommand: c.analysis.AdapterCommand})
	if replacementErr != nil || replacementProblem != nil {
		return nil, replacementProblem, replacementErr
	}
	c.analysisSession = replacement
	return replacement.CompileMany(ctx, selectors, &basis)
}

func affectedFlowIDs(previous []flowir.Document, basis flowir.Basis) []string {
	if len(previous) == 0 {
		return nil
	}
	if previous[0].Basis.HeadRevision != basis.HeadRevision {
		return documentFlowIDs(previous)
	}
	changed := changedManifestPaths(previous[0].Basis.Manifest, basis.Manifest)
	if len(changed) == 0 {
		return nil
	}
	for path := range changed {
		base := filepath.Base(path)
		if (strings.HasSuffix(path, ".dart") && !pathReferenced(previous, path)) || base == "pubspec.yaml" || base == "pubspec_overrides.yaml" || base == "analysis_options.yaml" || base == "codeflow.yaml" || base == "codeflow.external-contracts.json" {
			return documentFlowIDs(previous)
		}
	}
	result := make([]string, 0, len(previous))
	for _, document := range previous {
		dependencies := documentPaths(document)
		for path := range changed {
			if dependencies[path] {
				result = append(result, document.Current.ID)
				break
			}
		}
	}
	return result
}

func changedManifestPaths(before, after []flowir.ManifestEntry) map[string]bool {
	type identity struct{ kind, mode, hash, git string }
	old := map[string]identity{}
	for _, entry := range before {
		old[entry.Path] = identity{entry.Type, entry.Mode, entry.FileHash, entry.GitState}
	}
	changed := map[string]bool{}
	for _, entry := range after {
		current := identity{entry.Type, entry.Mode, entry.FileHash, entry.GitState}
		if prior, ok := old[entry.Path]; !ok || prior != current {
			changed[entry.Path] = true
		}
		delete(old, entry.Path)
	}
	for path := range old {
		changed[path] = true
	}
	return changed
}

func documentPaths(document flowir.Document) map[string]bool {
	result := map[string]bool{}
	add := func(anchors []flowir.Anchor) {
		for _, anchor := range anchors {
			if anchor.Kind == "code" || anchor.Kind == "config" || anchor.Kind == "test" || anchor.Kind == "contract" {
				result[anchor.Path] = true
			}
		}
	}
	for _, fact := range document.Facts {
		add(fact.Evidence)
	}
	for _, edge := range document.CausalEdges {
		add(edge.Evidence)
	}
	for _, step := range document.Current.Steps {
		add(step.PrimaryEvidence)
		for _, branch := range step.Branches {
			add(branch.Evidence)
		}
	}
	for _, unknown := range document.Unknowns {
		add(unknown.Evidence)
	}
	return result
}

func pathReferenced(documents []flowir.Document, path string) bool {
	for _, document := range documents {
		if documentPaths(document)[path] {
			return true
		}
	}
	return false
}

func documentFlowIDs(documents []flowir.Document) []string {
	result := make([]string, len(documents))
	for i := range documents {
		result[i] = documents[i].Current.ID
	}
	return result
}

func mergeReconciled(previous, compiled []flowir.Document, basis flowir.Basis) ([]flowir.Document, error) {
	byID := map[string]flowir.Document{}
	for _, document := range compiled {
		byID[document.Current.ID] = document
	}
	result := make([]flowir.Document, len(previous))
	for i, document := range previous {
		if replacement, ok := byID[document.Current.ID]; ok {
			result[i] = replacement
			delete(byID, document.Current.ID)
			continue
		}
		if err := anchorsMatchBasis(document, basis); err != nil {
			return nil, fmt.Errorf("rebase unaffected flow %s: %w", document.Current.ID, err)
		}
		document.Basis = basis
		if err := flowir.Validate(document); err != nil {
			return nil, fmt.Errorf("rebase unaffected flow %s: %w", document.Current.ID, err)
		}
		result[i] = document
	}
	if len(byID) > 0 {
		return nil, fmt.Errorf("analysis returned a flow outside the active workspace")
	}
	return result, nil
}

func anchorsMatchBasis(document flowir.Document, basis flowir.Basis) error {
	hashes := make(map[string]string, len(basis.Manifest))
	for _, entry := range basis.Manifest {
		if entry.Type == "file" {
			hashes[entry.Path] = entry.FileHash
		}
	}
	for path := range documentPaths(document) {
		expected := hashes[path]
		if expected == "" {
			return fmt.Errorf("evidence path %s is absent from the current manifest", path)
		}
		for _, anchor := range documentAnchors(document) {
			if anchor.Path == path && (anchor.Kind == "code" || anchor.Kind == "config" || anchor.Kind == "test" || anchor.Kind == "contract") && anchor.FileHash != expected {
				return fmt.Errorf("evidence path %s has stale file hash", path)
			}
		}
	}
	return nil
}

func documentAnchors(document flowir.Document) []flowir.Anchor {
	var anchors []flowir.Anchor
	for _, fact := range document.Facts {
		anchors = append(anchors, fact.Evidence...)
	}
	for _, edge := range document.CausalEdges {
		anchors = append(anchors, edge.Evidence...)
	}
	for _, step := range document.Current.Steps {
		anchors = append(anchors, step.PrimaryEvidence...)
		for _, branch := range step.Branches {
			anchors = append(anchors, branch.Evidence...)
		}
	}
	for _, unknown := range document.Unknowns {
		anchors = append(anchors, unknown.Evidence...)
	}
	return anchors
}

func (c *Core) Close(ctx context.Context) error {
	if c.watcher != nil {
		_ = c.watcher.Close()
	}
	c.reconcileMu.Lock()
	c.closed = true
	if c.reconcileTimer != nil && c.reconcileTimer.Stop() {
		c.reconcileWG.Done()
	}
	c.reconcileMu.Unlock()
	c.reconcileWG.Wait()
	err := c.server.Shutdown(ctx)
	if c.analysisSession != nil {
		if sessionErr := c.analysisSession.Close(ctx); err == nil {
			err = sessionErr
		}
	}
	c.store.Close()
	c.runtime.Release()
	return err
}

// Document returns the same published document served from the authenticated
// API. It is used by the short-lived `codeflow analyze` public CLI path.
func (c *Core) Document(ctx context.Context) (flowir.Document, error) {
	documents, _, _, err := c.store.GetBatch(ctx)
	if err != nil {
		return flowir.Document{}, err
	}
	return documents[0], nil
}
func (c *Core) Documents(ctx context.Context) ([]flowir.Document, error) {
	documents, _, _, err := c.store.GetBatch(ctx)
	return documents, err
}
func (c *Core) Workspace(ctx context.Context) (any, error) {
	documents, _, _, err := c.store.GetBatch(ctx)
	if err != nil {
		return nil, err
	}
	return buildWorkspace(documents)
}
func (c *Core) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", c.view)
	mux.HandleFunc("/api/v1/flows/", c.flow)
	mux.HandleFunc("/api/v1/workspace", c.workspace)
	mux.HandleFunc("/api/v2/workspace", c.workspaceV2)
	mux.HandleFunc("/api/v1/compare", c.compare)
	mux.HandleFunc("/api/v1/entry-points/resolve", c.resolve)
	mux.HandleFunc("/api/v1/refresh", c.refresh)
	mux.HandleFunc("/api/v1/overlay", c.overlay)
	mux.HandleFunc("/api/v1/overlay/import", c.importOverlay)
	mux.HandleFunc("/api/v1/overlay/approve", c.approveOverlay)
	mux.HandleFunc("/api/v1/domain-labels", c.domainLabels)
	mux.HandleFunc("/api/v1/export", c.export)
	mux.HandleFunc("/api/v1/debt", c.debt)
	mux.HandleFunc("/api/v1/debt/review", c.reviewDebt)
	mux.HandleFunc("/_codeflow/publication", c.publication)
	return c.localSecurity(mux)
}

func (c *Core) localSecurity(next http.Handler) http.Handler {
	expectedHost := strings.TrimPrefix(c.URL, "http://")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The listener is already TCP4 loopback-only. The Host check additionally
		// rejects browser DNS-rebinding requests that resolve an unrelated origin
		// to 127.0.0.1.
		if r.Host != expectedHost {
			http.Error(w, "CodeFlow is available only on its local review address", http.StatusMisdirectedRequest)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func localHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
}

func (c *Core) publication(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET is required", http.StatusMethodNotAllowed)
		return
	}
	publication, err := c.store.Publication(r.Context())
	if err != nil {
		http.Error(w, "publication unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(publication)
}
func (c *Core) debt(w http.ResponseWriter, r *http.Request) {
	if !c.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "UNAUTHORIZED", Message: "a valid CodeFlow runtime token is required"}})
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "METHOD_NOT_ALLOWED", Message: "GET is required"}})
		return
	}
	flowID := r.URL.Query().Get("flow_id")
	if flowID == "" {
		flowID = c.flowID
	}
	reviews, err := c.store.DebtReviews(r.Context(), flowID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "DEBT_REVIEW_UNAVAILABLE", Message: err.Error()}})
		return
	}
	writeJSON(w, http.StatusOK, envelope{Status: "ready", Data: reviews, Reviews: reviews, Unknowns: []any{}, ViewURL: c.URL + "/"})
}
func (c *Core) reviewDebt(w http.ResponseWriter, r *http.Request) {
	if !c.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "UNAUTHORIZED", Message: "a valid CodeFlow runtime token is required"}})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "METHOD_NOT_ALLOWED", Message: "POST is required"}})
		return
	}
	var request struct {
		ID     string `json:"id"`
		State  string `json:"state"`
		FlowID string `json:"flow_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&request); err != nil || request.ID == "" {
		writeJSON(w, http.StatusBadRequest, envelope{Status: "unknown", Unknowns: []any{}, Error: &apiError{Code: "DEBT_REVIEW_MALFORMED", Message: "id and state are required"}})
		return
	}
	if request.FlowID == "" {
		request.FlowID = c.flowID
	}
	if err := c.store.ReviewDebt(r.Context(), request.FlowID, request.ID, request.State, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		code, status := "DEBT_REVIEW_INVALID", http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			code, status = "DEBT_NOT_FOUND", http.StatusNotFound
		}
		writeJSON(w, status, envelope{Status: "unknown", Unknowns: []any{}, Error: &apiError{Code: code, Message: err.Error()}})
		return
	}
	reviews, _ := c.store.DebtReviews(r.Context(), request.FlowID)
	writeJSON(w, http.StatusOK, envelope{Status: "ready", Data: reviews, Reviews: reviews, Unknowns: []any{}, ViewURL: c.URL + "/"})
}
func (c *Core) overlay(w http.ResponseWriter, r *http.Request) {
	if !c.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "UNAUTHORIZED", Message: "a valid CodeFlow runtime token is required"}})
		return
	}
	if r.Method == http.MethodDelete {
		c.overlayMu.Lock()
		c.overlays = nil
		c.overlayMu.Unlock()
		writeJSON(w, http.StatusOK, envelope{Status: "ready", Data: []ontology.Candidate{}, Unknowns: []any{}, ViewURL: c.URL + "/"})
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "METHOD_NOT_ALLOWED", Message: "GET or DELETE is required"}})
		return
	}
	c.overlayMu.Lock()
	candidates := append([]ontology.Candidate(nil), c.overlays...)
	c.overlayMu.Unlock()
	writeJSON(w, http.StatusOK, envelope{Status: "ready", Data: candidates, Unknowns: []any{}, ViewURL: c.URL + "/"})
}
func (c *Core) importOverlay(w http.ResponseWriter, r *http.Request) {
	if !c.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "UNAUTHORIZED", Message: "a valid CodeFlow runtime token is required"}})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "METHOD_NOT_ALLOWED", Message: "POST is required"}})
		return
	}
	candidates, err := ontology.Ingest(io.LimitReader(r.Body, 1024*1024))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{Status: "unknown", Unknowns: []any{}, Error: &apiError{Code: "OVERLAY_MALFORMED", Message: err.Error()}})
		return
	}
	c.overlayMu.Lock()
	c.overlays = candidates
	c.overlayMu.Unlock()
	writeJSON(w, http.StatusOK, envelope{Status: "ready", Data: candidates, Unknowns: []any{}, ViewURL: c.URL + "/"})
}
func (c *Core) approveOverlay(w http.ResponseWriter, r *http.Request) {
	if !c.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "UNAUTHORIZED", Message: "a valid CodeFlow runtime token is required"}})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "METHOD_NOT_ALLOWED", Message: "POST is required"}})
		return
	}
	var request struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{Status: "unknown", Unknowns: []any{}, Error: &apiError{Code: "OVERLAY_MALFORMED", Message: "id is required"}})
		return
	}
	c.overlayMu.Lock()
	defer c.overlayMu.Unlock()
	for i := range c.overlays {
		if c.overlays[i].ID == request.ID {
			confirmed, err := ontology.Approve(c.repo, c.overlays[i])
			if err != nil {
				writeJSON(w, http.StatusBadRequest, envelope{Status: "unknown", Unknowns: []any{}, Error: &apiError{Code: "OVERLAY_NOT_APPROVABLE", Message: err.Error()}})
				return
			}
			c.overlays[i].Status = "confirmed"
			writeJSON(w, http.StatusOK, envelope{Status: "ready", Data: confirmed, Unknowns: []any{}, ViewURL: c.URL + "/"})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, envelope{Status: "unknown", Unknowns: []any{}, Error: &apiError{Code: "OVERLAY_NOT_FOUND", Message: "candidate is not present in this runtime"}})
}

// domainLabels exposes the reviewed wording layer without granting it any
// authority over FlowIR. A PUT is an explicit local approval: its target must
// exist in the current published document, so stale code cannot receive a
// previously approved business explanation.
func (c *Core) domainLabels(w http.ResponseWriter, r *http.Request) {
	if !c.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "UNAUTHORIZED", Message: "a valid CodeFlow runtime token is required"}})
		return
	}
	c.domainMu.Lock()
	defer c.domainMu.Unlock()
	switch r.Method {
	case http.MethodGet:
		labels, err := ontology.LoadDomainLabels(c.repo)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "DOMAIN_LABELS_UNAVAILABLE", Message: err.Error()}})
			return
		}
		writeJSON(w, http.StatusOK, envelope{Status: "ready", Data: labels, Unknowns: []any{}, ViewURL: c.URL + "/"})
	case http.MethodPut:
		var label ontology.DomainLabel
		if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&label); err != nil {
			writeJSON(w, http.StatusBadRequest, envelope{Status: "unknown", Unknowns: []any{}, Error: &apiError{Code: "DOMAIN_LABEL_MALFORMED", Message: "flow_id, scenario_id, optional step_id, and title are required"}})
			return
		}
		documents, _, _, err := c.store.GetBatch(r.Context())
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "DOMAIN_LABELS_UNAVAILABLE", Message: "current flow workspace is unavailable"}})
			return
		}
		if !domainLabelTargetExists(documents, label) {
			writeJSON(w, http.StatusBadRequest, envelope{Status: "unknown", Unknowns: []any{}, Error: &apiError{Code: "DOMAIN_LABEL_TARGET_INVALID", Message: "the label target is not present in the current observed flow"}})
			return
		}
		stored, err := ontology.SaveDomainLabel(c.repo, label)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, envelope{Status: "unknown", Unknowns: []any{}, Error: &apiError{Code: "DOMAIN_LABEL_INVALID", Message: err.Error()}})
			return
		}
		writeJSON(w, http.StatusOK, envelope{Status: "ready", Data: stored, Unknowns: []any{}, ViewURL: c.URL + "/"})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "METHOD_NOT_ALLOWED", Message: "GET or PUT is required"}})
	}
}

func domainLabelTargetExists(documents []flowir.Document, label ontology.DomainLabel) bool {
	for _, document := range documents {
		if document.Current.ID != label.FlowID {
			continue
		}
		for _, scenario := range document.Scenarios {
			if scenario.ID != label.ScenarioID {
				continue
			}
			if label.StepID == "" {
				return true
			}
			for _, stepID := range scenario.StepIDs {
				if stepID == label.StepID {
					return true
				}
			}
		}
	}
	return false
}

// export writes a self-contained review document that is safe to attach to a
// pull request. It contains the captured basis and evidence snippets, but no
// local runtime polling, auth token, or vscode:// link that would be invalid
// for another reviewer.
func (c *Core) export(w http.ResponseWriter, r *http.Request) {
	if !c.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "UNAUTHORIZED", Message: "a valid CodeFlow runtime token is required"}})
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "METHOD_NOT_ALLOWED", Message: "GET is required"}})
		return
	}
	bytes, err := c.ExportHTML(r.Context(), r.URL.Query().Get("flow_id"), r.URL.Query().Get("scenario"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{Status: "unknown", Unknowns: []any{}, Error: &apiError{Code: "EXPORT_UNAVAILABLE", Message: err.Error()}})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=codeflow-flow.html")
	_, _ = w.Write(bytes)
}

// ExportHTML returns one immutable-screen report. Scenario selection is
// explicit in the report metadata; when omitted, it matches FlowView's first
// source-backed interaction selection.
func (c *Core) ExportHTML(ctx context.Context, flowID, scenarioID string) ([]byte, error) {
	documents, publishedAt, status, err := c.store.GetBatch(ctx)
	if err != nil {
		return nil, fmt.Errorf("workspace unavailable: %w", err)
	}
	workspace, err := buildWorkspace(documents)
	if err != nil {
		return nil, fmt.Errorf("workspace invalid: %w", err)
	}
	if flowID == "" {
		flowID = documents[0].Current.ID
	}
	for _, document := range documents {
		if document.Current.ID != flowID {
			continue
		}
		model, err := c.flowViewModel(ctx, document, status, workspace, publishedAt, scenarioID, entrypoint.Result{Candidates: []entrypoint.EntryPoint{}}, true)
		if err != nil {
			return nil, err
		}
		var out bytes.Buffer
		if err := exportPage.Execute(&out, model); err != nil {
			return nil, fmt.Errorf("render export: %w", err)
		}
		return out.Bytes(), nil
	}
	return nil, fmt.Errorf("flow %q is not present in the current workspace", flowID)
}
func (c *Core) refresh(w http.ResponseWriter, r *http.Request) {
	if !c.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "UNAUTHORIZED", Message: "a valid CodeFlow runtime token is required"}})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "METHOD_NOT_ALLOWED", Message: "POST is required"}})
		return
	}
	if err := c.Refresh(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "REFRESH_FAILED", Message: err.Error()}})
		return
	}
	documents, _, status, err := c.store.GetBatch(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "STORAGE_ERROR", Message: err.Error()}})
		return
	}
	unknowns := make([]any, 0)
	for _, document := range documents {
		for _, unknown := range document.Unknowns {
			unknowns = append(unknowns, unknown)
		}
	}
	if len(documents) > 1 {
		workspace, buildErr := buildWorkspace(documents)
		if buildErr != nil {
			writeJSON(w, http.StatusInternalServerError, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "WORKSPACE_INVALID", Message: buildErr.Error()}})
			return
		}
		writeJSON(w, http.StatusOK, envelope{Basis: &workspace.Basis, Status: status, Data: workspace, Unknowns: unknowns, ViewURL: c.URL + "/"})
		return
	}
	document := documents[0]
	writeJSON(w, http.StatusOK, envelope{Basis: &document.Basis, Status: status, Data: document, Unknowns: unknowns, ViewURL: c.URL + "/"})
}
func (c *Core) compare(w http.ResponseWriter, r *http.Request) {
	if !c.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "UNAUTHORIZED", Message: "a valid CodeFlow runtime token is required"}})
		return
	}
	if c.comparison == nil {
		writeJSON(w, http.StatusNotFound, envelope{Status: "unknown", Unknowns: []any{}, Error: &apiError{Code: "BASELINE_NOT_SELECTED", Message: "start CodeFlow with a baseline revision"}})
		return
	}
	unknowns := make([]any, len(c.comparison.Current.Unknowns))
	for i := range c.comparison.Current.Unknowns {
		unknowns[i] = c.comparison.Current.Unknowns[i]
	}
	writeJSON(w, http.StatusOK, envelope{Basis: &c.comparison.Current.Basis, Status: "ready", Data: c.comparison, Unknowns: unknowns, ViewURL: c.URL + "/", Metadata: metadata{RuntimeStatus: "ready", ViewURL: c.URL + "/"}})
}
func (c *Core) resolve(w http.ResponseWriter, r *http.Request) {
	if !c.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "UNAUTHORIZED", Message: "a valid CodeFlow runtime token is required"}})
		return
	}
	result := entrypoint.Resolve(r.Context(), c.repo, r.URL.Query().Get("selector"), c.adapterCommand)
	unknowns := []any{}
	if result.Unknown != nil {
		unknowns = []any{result.Unknown}
	}
	code := http.StatusOK
	if result.State == entrypoint.Unavailable {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, envelope{Status: string(result.State), Unknowns: unknowns, Data: result, ViewURL: c.URL + "/?selector=" + r.URL.Query().Get("selector"), Metadata: metadata{RuntimeStatus: string(result.State), ViewURL: c.URL + "/"}})
}
func (c *Core) authorized(r *http.Request) bool {
	return secureToken(r.Header.Get("X-CodeFlow-Token"), c.Token) || secureToken(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), c.Token)
}

func secureToken(candidate, expected string) bool {
	return len(candidate) == len(expected) && len(expected) > 0 && subtle.ConstantTimeCompare([]byte(candidate), []byte(expected)) == 1
}
func (c *Core) flow(w http.ResponseWriter, r *http.Request) {
	if !c.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, envelope{Status: "unavailable", Unknowns: []any{}, Metadata: metadata{RuntimeStatus: "unavailable"}, Error: &apiError{Code: "UNAUTHORIZED", Message: "a valid CodeFlow runtime token is required"}})
		return
	}
	flowID := r.URL.Query().Get("id")
	if flowID == "" {
		flowID = path.Base(r.URL.Path)
	}
	document, generated, status, err := c.store.Get(r.Context(), flowID)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, envelope{Status: "ready", Unknowns: []any{}, Metadata: metadata{RuntimeStatus: "ready"}, Error: &apiError{Code: "FLOW_NOT_FOUND", Message: "flow was not found"}})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, envelope{Status: "unavailable", Unknowns: []any{}, Metadata: metadata{RuntimeStatus: "unavailable"}, Error: &apiError{Code: "STORAGE_ERROR", Message: "stored flow could not be read"}})
		return
	}
	viewURL := c.URL + "/"
	if documents, _, _, batchErr := c.store.GetBatch(r.Context()); batchErr == nil && len(documents) > 1 {
		viewURL += "?flow=" + url.QueryEscape(document.Current.ID)
	}
	unknowns := make([]any, len(document.Unknowns))
	for i := range document.Unknowns {
		unknowns[i] = document.Unknowns[i]
	}
	reviews, _ := c.store.DebtReviews(r.Context(), document.Current.ID)
	writeJSON(w, http.StatusOK, envelope{Basis: &document.Basis, Status: status, Data: document, Unknowns: unknowns, Reviews: reviews, Lenses: documentLenses(document), ViewURL: viewURL, Metadata: metadata{GeneratedAt: generated, RuntimeStatus: status, ViewURL: viewURL}})
}

func (c *Core) workspace(w http.ResponseWriter, r *http.Request) {
	if !c.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "UNAUTHORIZED", Message: "a valid CodeFlow runtime token is required"}})
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "METHOD_NOT_ALLOWED", Message: "GET is required"}})
		return
	}
	documents, generated, status, err := c.store.GetBatch(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "STORAGE_ERROR", Message: "stored workspace could not be read"}})
		return
	}
	workspace, err := buildWorkspace(documents)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "WORKSPACE_INVALID", Message: err.Error()}})
		return
	}
	unknowns := workspaceUnknowns(documents)
	viewURL := c.URL + "/"
	writeJSON(w, http.StatusOK, envelope{Basis: &workspace.Basis, Status: status, Data: workspace, Unknowns: unknowns, ViewURL: viewURL, Metadata: metadata{GeneratedAt: generated, RuntimeStatus: status, ViewURL: viewURL}})
}

func (c *Core) workspaceV2(w http.ResponseWriter, r *http.Request) {
	if !c.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, workspaceEnvelopeV2{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "UNAUTHORIZED", Message: "a valid CodeFlow runtime token is required"}})
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, workspaceEnvelopeV2{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "METHOD_NOT_ALLOWED", Message: "GET is required"}})
		return
	}
	documents, generated, status, err := c.store.GetBatch(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, workspaceEnvelopeV2{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "STORAGE_ERROR", Message: "stored workspace could not be read"}})
		return
	}
	workspace, err := buildWorkspaceV2(documents)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, workspaceEnvelopeV2{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "WORKSPACE_INVALID", Message: err.Error()}})
		return
	}
	basis := documents[0].Basis
	viewURL := c.URL + "/"
	writeJSON(w, http.StatusOK, workspaceEnvelopeV2{
		Basis:  workspaceBasisV2{Repository: basis.Repository, HeadRevision: basis.HeadRevision, BaselineRevision: basis.BaselineRevision, WorktreeFingerprint: basis.WorktreeFingerprint, Dirty: basis.Dirty, ManifestCount: len(basis.Manifest)},
		Status: status, Data: workspace, Unknowns: workspaceUnknowns(documents), ViewURL: viewURL,
		Metadata: metadata{GeneratedAt: generated, RuntimeStatus: status, ViewURL: viewURL},
	})
}

func workspaceUnknowns(documents []flowir.Document) []any {
	unknowns := make([]any, 0)
	for _, document := range documents {
		for _, unknown := range document.Unknowns {
			unknowns = append(unknowns, struct {
				FlowID  string               `json:"flow_id"`
				Unknown flowir.UnknownDetail `json:"unknown"`
			}{document.Current.ID, unknown})
		}
	}
	return unknowns
}

func buildWorkspace(documents []flowir.Document) (workspaceDocument, error) {
	if len(documents) == 0 {
		return workspaceDocument{}, fmt.Errorf("workspace has no flows")
	}
	basis := documents[0].Basis
	flowIDs := make([]string, len(documents))
	targets := map[string]bool{}
	for i, document := range documents {
		if !flowir.SameBasis(document.Basis, basis) {
			return workspaceDocument{}, fmt.Errorf("workspace contains mixed flow bases")
		}
		flowIDs[i] = document.Current.ID
		targets[document.Current.ID] = true
	}
	seen := map[string]bool{}
	edges := make([]workspaceFlowEdge, 0)
	for _, document := range documents {
		for _, fact := range document.Facts {
			if fact.Status != flowir.Observed || (fact.Kind != "visible_result" && fact.Kind != "route_transition") || !targets[fact.Object] || fact.Object == document.Current.ID {
				continue
			}
			key := document.Current.ID + "\x00" + fact.Object
			if seen[key] {
				continue
			}
			seen[key] = true
			edges = append(edges, workspaceFlowEdge{ID: flowir.Hash("screen_flow_edge", document.Current.ID, fact.Object), From: document.Current.ID, To: fact.Object, Status: flowir.Observed, Evidence: append([]flowir.Anchor(nil), fact.Evidence...)})
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		return edges[i].From < edges[j].From || edges[i].From == edges[j].From && edges[i].To < edges[j].To
	})
	return workspaceDocument{SchemaVersion: "1", Basis: basis, FlowIDs: flowIDs, Flows: documents, Edges: edges}, nil
}

func buildWorkspaceV2(documents []flowir.Document) (workspaceDocumentV2, error) {
	workspace, err := buildWorkspace(documents)
	if err != nil {
		return workspaceDocumentV2{}, err
	}
	flows := make([]workspaceFlowDocumentV2, len(documents))
	for i, document := range documents {
		flows[i] = workspaceFlowDocumentV2{
			SchemaVersion: document.SchemaVersion,
			Facts:         document.Facts,
			CausalEdges:   document.CausalEdges,
			Architecture:  document.Architecture,
			Current:       document.Current,
			Scenarios:     document.Scenarios,
			Unknowns:      document.Unknowns,
		}
	}
	return workspaceDocumentV2{SchemaVersion: "2", FlowIDs: workspace.FlowIDs, Flows: flows, Edges: workspace.Edges}, nil
}

func buildWorkspaceNavigation(workspace workspaceDocument, selected string) workspaceNavigation {
	flows := make([]workspaceNavigationFlow, 0, len(workspace.Flows))
	for _, document := range workspace.Flows {
		flows = append(flows, workspaceNavigationFlow{ID: document.Current.ID, URL: "/?flow=" + url.QueryEscape(document.Current.ID), Status: document.Current.Status, Steps: len(document.Current.Steps), Unknowns: len(document.Unknowns), Selected: document.Current.ID == selected})
	}
	return workspaceNavigation{Flows: flows, Edges: workspace.Edges}
}

func scenarioTitle(document flowir.Document, scenario flowir.Scenario, labels map[string]ontology.DomainLabel) string {
	if label, ok := labels[ontology.DomainLabelID(document.Current.ID, scenario.ID, "")]; ok {
		return label.Title
	}
	for _, fact := range document.Facts {
		if fact.ID != scenario.InteractionFact {
			continue
		}
		if fact.Object != "" {
			return fact.Object
		}
		return humanAction(fact.Subject)
	}
	return "사용자 경로"
}

func buildScenarioNavigation(document flowir.Document, selected string, labels map[string]ontology.DomainLabel) scenarioNavigation {
	items := make([]scenarioNavigationItem, 0, len(document.Scenarios))
	for _, scenario := range document.Scenarios {
		unknowns := 0
		stepSet := map[string]bool{}
		for _, stepID := range scenario.StepIDs {
			stepSet[stepID] = true
		}
		for _, unknown := range document.Unknowns {
			for _, stepID := range unknown.RelatedSteps {
				if stepSet[stepID] {
					unknowns++
					break
				}
			}
		}
		item := scenarioNavigationItem{ID: scenario.ID, URL: "/?flow=" + url.QueryEscape(document.Current.ID) + "&scenario=" + url.QueryEscape(scenario.ID), Title: scenarioTitle(document, scenario, labels), Status: scenario.Status, Steps: len(scenario.StepIDs), Unknowns: unknowns, Selected: scenario.ID == selected}
		items = append(items, item)
	}
	if selected == "" && len(items) > 0 {
		items[0].Selected = true
		selected = items[0].ID
	}
	var active *scenarioNavigationItem
	for i := range items {
		if items[i].ID == selected {
			active = &items[i]
			break
		}
	}
	return scenarioNavigation{Flows: items, Selected: active}
}

func scopeScenario(document flowir.Document, scenarioID string) (flowir.Document, *flowir.Scenario) {
	if scenarioID == "" && len(document.Scenarios) > 0 {
		scenarioID = document.Scenarios[0].ID
	}
	for _, scenario := range document.Scenarios {
		if scenario.ID != scenarioID {
			continue
		}
		allowed := map[string]bool{}
		for _, stepID := range scenario.StepIDs {
			allowed[stepID] = true
		}
		scoped := document
		scoped.Current.Steps = nil
		scoped.Current.Status = scenario.Status
		for _, step := range document.Current.Steps {
			if allowed[step.ID] {
				scoped.Current.Steps = append(scoped.Current.Steps, step)
			}
		}
		scoped.Unknowns = nil
		for _, unknown := range document.Unknowns {
			for _, stepID := range unknown.RelatedSteps {
				if allowed[stepID] {
					scoped.Unknowns = append(scoped.Unknowns, unknown)
					break
				}
			}
		}
		scoped.Scenarios = []flowir.Scenario{scenario}
		return scoped, &scenario
	}
	return document, nil
}

func domainLabelMap(labels []ontology.DomainLabel) map[string]ontology.DomainLabel {
	indexed := make(map[string]ontology.DomainLabel, len(labels))
	for _, label := range labels {
		if label.Status == "confirmed" {
			indexed[label.ID] = label
		}
	}
	return indexed
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func documentLenses(document flowir.Document) []lens.Source {
	seen := map[string]bool{}
	var out []lens.Source
	for _, step := range document.Current.Steps {
		for _, anchor := range step.PrimaryEvidence {
			key := anchor.Path + fmt.Sprint(anchor.LineRange)
			if !seen[key] {
				out = append(out, lens.Read(document.Basis, anchor))
				seen[key] = true
			}
		}
	}
	return out
}

func stepLenses(document flowir.Document) map[string]lens.Source {
	out := map[string]lens.Source{}
	for _, step := range document.Current.Steps {
		if len(step.PrimaryEvidence) > 0 {
			out[step.ID] = lens.Read(document.Basis, step.PrimaryEvidence[0])
		}
	}
	return out
}

func factLabels(document flowir.Document) map[string]string {
	out := map[string]string{}
	for _, fact := range document.Facts {
		out[fact.ID] = displayFact(fact)
	}
	return out
}

func displayFact(fact flowir.Fact) string {
	value := fact.Object
	if value == "" {
		value = fact.Subject
	}
	switch fact.Kind {
	case "screen_entry":
		return "이 화면을 엽니다"
	case "user_action":
		if fact.Object != "" {
			return "“" + fact.Object + "”을 선택합니다"
		}
		return "사용자 동작 · " + humanAction(value)
	case "condition", "confirmation_condition":
		return humanCondition(value)
	case "provider_dependency":
		return "이 단계에 필요한 정보를 확인합니다"
	case "unknown_state":
		return "처리 상태가 어떻게 바뀌는지 확인할 수 없습니다"
	case "state_transition", "notifier_state_transition":
		return "처리 상태를 갱신합니다"
	case "event_dispatch":
		return "다음 처리를 요청합니다"
	case "terminal_result":
		return "이 단계에서 처리가 끝납니다"
	case "listener_condition":
		return "변경된 처리 상태를 확인합니다"
	case "route_transition", "visible_result":
		return "다음 화면으로 이동합니다"
	case "repository_access":
		return "필요한 정보를 저장하거나 조회합니다"
	case "external_call", "external_result":
		return "외부 서비스와 연결합니다"
	default:
		return "코드에서 확인한 처리를 진행합니다"
	}
}

func shortSymbol(value string) string {
	value = strings.TrimPrefix(value, "provider:")
	value = strings.TrimPrefix(value, "state:")
	value = strings.TrimPrefix(value, "event:")
	if strings.Contains(value, "::") {
		parts := strings.Split(value, "::")
		if len(parts) >= 3 {
			owner := parts[len(parts)-2]
			member := parts[len(parts)-1]
			if colon := strings.LastIndex(owner, ":"); colon >= 0 {
				owner = owner[colon+1:]
			}
			if colon := strings.LastIndex(member, ":"); colon >= 0 {
				member = member[colon+1:]
			}
			if owner == "top-level" {
				return member
			}
			return owner + "." + member
		}
	}
	// Expressions are evidence, not qualified symbols. Trimming at their last
	// dot can turn `state?.isCompleted ?? false` into the misleading
	// `isCompleted ?? false` in FlowView.
	if strings.ContainsAny(value, " \t?=|&!()") {
		return value
	}
	if at := strings.LastIndex(value, "."); at >= 0 && at+1 < len(value) {
		return value[at+1:]
	}
	return value
}

func humanAction(value string) string {
	name := strings.TrimLeft(shortSymbol(value), "_")
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		name = strings.TrimLeft(name[dot+1:], "_")
	}
	switch strings.ToLower(name) {
	case "requestexit", "exit", "close", "cancel", "back":
		return "나가기 요청"
	case "submit", "confirm":
		return "입력 내용 제출"
	case "continue", "next":
		return "다음 단계로 이동"
	case "save":
		return "변경 내용 저장"
	case "openhelp":
		return "도움말 열기"
	case "login", "signin":
		return "로그인"
	case "signup", "join":
		return "가입 요청"
	default:
		return humanIdentifier(name)
	}
}

func humanCondition(value string) string {
	normalized := strings.ToLower(strings.ReplaceAll(value, " ", ""))
	switch {
	case strings.Contains(normalized, "iscompleted"):
		return "이전 작업이 완료되었는지 확인합니다"
	case strings.Contains(normalized, "confirmed"):
		return "계속 진행해도 되는지 확인합니다"
	case strings.Contains(normalized, "iscanceled"):
		return "취소된 요청인지 확인합니다"
	default:
		return "계속 진행할 수 있는지 확인합니다"
	}
}

func humanIdentifier(value string) string {
	value = strings.TrimPrefix(value, "provider:")
	value = strings.TrimPrefix(value, "state:")
	value = strings.TrimPrefix(value, "event:")
	value = strings.Trim(value, "_")
	value = strings.NewReplacer(".", "_", ":", "_", "=", "_=_").Replace(value)
	if value == "" {
		return "이름 없는 동작"
	}
	var words []string
	start := 0
	runes := []rune(value)
	for i := 1; i < len(runes); i++ {
		if runes[i] == '_' || runes[i] == '-' {
			if start < i {
				words = append(words, string(runes[start:i]))
			}
			start = i + 1
			continue
		}
		if runes[i] >= 'A' && runes[i] <= 'Z' && runes[i-1] >= 'a' && runes[i-1] <= 'z' {
			words = append(words, string(runes[start:i]))
			start = i
		}
	}
	if start < len(runes) {
		words = append(words, string(runes[start:]))
	}
	if len(words) == 0 {
		return value
	}
	readable := make([]string, 0, len(words))
	for _, word := range words {
		switch strings.ToLower(word) {
		case "provider":
			continue
		case "is":
			continue
		case "join":
			readable = append(readable, "가입")
		case "cancel":
			readable = append(readable, "취소")
		case "canceled", "iscanceled":
			readable = append(readable, "취소됨")
		case "controller":
			readable = append(readable, "진행")
		case "route":
			readable = append(readable, "화면 경로")
		case "destination":
			readable = append(readable, "목적지")
		case "dispatcher":
			readable = append(readable, "이동 처리")
		case "state":
			readable = append(readable, "상태")
		case "event":
			readable = append(readable, "요청")
		case "auth":
			readable = append(readable, "인증")
		case "home":
			readable = append(readable, "홈")
		case "true":
			readable = append(readable, "예")
		case "false":
			readable = append(readable, "아니오")
		default:
			readable = append(readable, word)
		}
	}
	return strings.Join(readable, " ")
}

type timelineItem struct {
	Step             flowir.Step
	Title            string
	StateDelta       string
	StateBefore      string
	StateAfter       string
	StateProof       string
	CodeImpact       string
	ResultImpact     string
	HasStateChange   bool
	Change           string
	ChangeLabel      string
	SourceState      string
	SourceStateLabel string
	Lens             lens.Source
	EditorURL        template.URL
	Incoming         []causalView
	Outgoing         []causalView
	Branches         []branchView
	TrustLabel       string
	BranchPath       string
	BreakAfter       bool
	Alternative      bool
}

type branchView struct {
	ID        string
	Condition string
	Status    flowir.Status
	Outcomes  []branchOutcomeView
}

type branchOutcomeView struct {
	StepIndex int
	PathLabel string
	Title     string
}

func trustLabel(status flowir.Status) string {
	switch status {
	case flowir.Observed:
		return "코드에서 확인됨"
	case flowir.Mixed:
		return "일부 연결 미확정"
	case flowir.Unknown:
		return "다음 동작 미확정"
	case flowir.Stale:
		return "코드 변경으로 재확인 필요"
	default:
		return string(status)
	}
}

func displayChange(change string) string {
	switch change {
	case "current":
		return "비교 기준 없음"
	case "unchanged":
		return "행동 변화 없음"
	case "changed":
		return "행동 변경"
	case "added":
		return "새 행동"
	default:
		return change
	}
}

type causalView struct {
	Kind, RawKind, Label string
	Status               flowir.Status
}

func displayCausalKind(kind string) string {
	switch kind {
	case "enters":
		return "흐름 시작"
	case "causes":
		return "직접 원인"
	case "changes_state":
		return "상태 변경"
	case "observed_by":
		return "상태 감지"
	case "produces":
		return "결과 생성"
	case "guards":
		return "조건 분기"
	case "permits":
		return "조건 통과"
	default:
		return kind
	}
}

func scenarioIDForStep(document flowir.Document, stepID string) string {
	for _, scenario := range document.Scenarios {
		for _, candidate := range scenario.StepIDs {
			if candidate == stepID {
				return scenario.ID
			}
		}
	}
	return ""
}

func timeline(document flowir.Document, comparison *compare.Result) []timelineItem {
	return timelineWithDomainLabels(document, comparison, nil)
}

func timelineWithDomainLabels(document flowir.Document, comparison *compare.Result, domainLabels map[string]ontology.DomainLabel) []timelineItem {
	factLabel := factLabels(document)
	changed := map[string]string{}
	if comparison != nil {
		for _, id := range comparison.Delta.AddedSteps {
			changed[id] = "added"
		}
		for _, pair := range append(append([]delta.Pair{}, comparison.Delta.ChangedResults...), comparison.Delta.ChangedBranches...) {
			if pair.After != "" {
				changed[pair.After] = "changed"
			}
		}
		for _, pair := range comparison.Delta.ChangedStates {
			if pair.After != "" {
				changed[pair.After] = "changed"
			}
		}
	}
	manifest := map[string]flowir.ManifestEntry{}
	for _, entry := range document.Basis.Manifest {
		manifest[entry.Path] = entry
	}
	items := make([]timelineItem, 0, len(document.Current.Steps))
	for _, step := range document.Current.Steps {
		change := "current"
		if comparison != nil {
			change = "unchanged"
			if value := changed[step.ID]; value != "" {
				change = value
			}
		}
		title := "다음 처리 결과를 확인할 수 없습니다"
		ids := append(append([]string{}, step.BehaviorFacts...), step.ResultFacts...)
		if len(ids) > 0 {
			for _, fact := range document.Facts {
				if fact.ID == ids[len(ids)-1] {
					title = displayFact(fact)
					break
				}
			}
		}
		if label, ok := domainLabels[ontology.DomainLabelID(document.Current.ID, scenarioIDForStep(document, step.ID), step.ID)]; ok {
			title = label.Title
		}
		sourceState := "unknown"
		var source lens.Source
		if len(step.PrimaryEvidence) > 0 {
			source = lens.Read(document.Basis, step.PrimaryEvidence[0])
			if entry, ok := manifest[step.PrimaryEvidence[0].Path]; ok {
				sourceState = entry.GitState
			}
		}
		facts := map[string]bool{}
		for _, id := range ids {
			facts[id] = true
		}
		if len(facts) == 0 {
			facts[step.TriggerFact] = true
		}
		item := timelineItem{Step: step, Title: title, Change: change, ChangeLabel: displayChange(change), SourceState: sourceState, SourceStateLabel: displaySourceState(sourceState), Lens: source, TrustLabel: trustLabel(step.Status)}
		if source.Status == "ready" && source.EditorURL != "" {
			item.EditorURL = template.URL(source.EditorURL)
		}
		for _, edge := range document.CausalEdges {
			fromCurrent, toCurrent := facts[edge.FromFact], facts[edge.ToFact]
			if toCurrent && !fromCurrent {
				item.Incoming = append(item.Incoming, causalView{Kind: displayCausalKind(edge.Kind), RawKind: edge.Kind, Label: factLabel[edge.FromFact], Status: edge.Status})
			}
			if fromCurrent && !toCurrent {
				item.Outgoing = append(item.Outgoing, causalView{Kind: displayCausalKind(edge.Kind), RawKind: edge.Kind, Label: factLabel[edge.ToFact], Status: edge.Status})
			}
		}
		item.StateDelta = title
		item.StateBefore, item.StateAfter, item.StateProof = stepStateDelta(step, facts, document.Facts)
		item.HasStateChange = stepChangesState(facts, document.Facts)
		if comparison != nil {
			item.CodeImpact = item.ChangeLabel
		} else if sourceState == "added" || sourceState == "modified" || sourceState == "renamed" || sourceState == "untracked" {
			item.CodeImpact = displaySourceState(sourceState) + " · 기준선 미선택"
		} else {
			item.CodeImpact = "현재 코드 · 기준선 미선택"
		}
		item.ResultImpact = visibleResultForStep(step, document.Facts)
		for _, incoming := range item.Incoming {
			if incoming.RawKind == "changes_state" || incoming.RawKind == "produces" {
				item.StateDelta = incoming.Label + " → " + title
				break
			}
		}
		for _, outgoing := range item.Outgoing {
			if outgoing.RawKind == "changes_state" || outgoing.RawKind == "observed_by" || outgoing.RawKind == "produces" {
				item.StateDelta += " → " + outgoing.Label
				break
			}
		}
		items = append(items, item)
	}
	stepIndexes := map[string]int{}
	for index, item := range items {
		stepIndexes[item.Step.ID] = index
	}
	branchNumber := 0
	for i := range items {
		for _, branch := range items[i].Step.Branches {
			branchNumber++
			view := branchView{ID: branch.ID, Condition: factLabel[branch.ConditionFact], Status: branch.Status}
			previousIndex := -1
			for outcomeIndex, outcome := range branch.OutcomeStepIDs {
				if index, ok := stepIndexes[outcome]; ok {
					pathLabel := fmt.Sprintf("분기 %d · 경로 %c", branchNumber, 'A'+rune(outcomeIndex))
					view.Outcomes = append(view.Outcomes, branchOutcomeView{StepIndex: index, PathLabel: pathLabel, Title: items[index].Title})
					items[index].BranchPath = pathLabel
					if previousIndex >= 0 && previousIndex+1 == index {
						items[previousIndex].BreakAfter = true
						items[index].Alternative = true
					}
					previousIndex = index
				}
			}
			items[i].Branches = append(items[i].Branches, view)
		}
	}
	return items
}

func displaySourceState(state string) string {
	switch state {
	case "clean":
		return "Git 변경 없음"
	case "added":
		return "Git에 추가됨"
	case "modified":
		return "수정됨"
	case "renamed":
		return "이름 변경됨"
	case "deleted":
		return "삭제됨"
	case "untracked":
		return "Git에 아직 추가되지 않음"
	default:
		return "Git 상태 미확정"
	}
}

func stepChangesState(referenced map[string]bool, facts []flowir.Fact) bool {
	for _, fact := range facts {
		if referenced[fact.ID] && (fact.Kind == "state_transition" || fact.Kind == "notifier_state_transition") {
			return true
		}
	}
	return false
}

func visibleResultForStep(step flowir.Step, facts []flowir.Fact) string {
	byID := map[string]flowir.Fact{}
	for _, fact := range facts {
		byID[fact.ID] = fact
	}
	for _, id := range step.ResultFacts {
		fact := byID[id]
		if fact.Kind == "visible_result" {
			return strings.TrimPrefix(fact.Object, "route:")
		}
	}
	return "이 단계에서 직접 결과 없음"
}

func stepStateDelta(step flowir.Step, referenced map[string]bool, all []flowir.Fact) (before, after, proof string) {
	before, after, proof = "상태 입력 없음", "상태 변경 없음", "현재 단계에서 상태 전이를 주장하지 않음"
	for _, fact := range all {
		if !referenced[fact.ID] {
			continue
		}
		switch fact.Kind {
		case "state_transition", "notifier_state_transition":
			return "대입 전 값은 코드만으로 확인되지 않음", humanIdentifier(strings.TrimPrefix(fact.Object, "state:")), "현재 코드에서 대입 결과를 확인했습니다. 대입 직전 값은 코드만으로 확인되지 않습니다."
		case "listener_condition":
			return humanIdentifier(strings.TrimPrefix(fact.Object, "state.")), "값은 바뀌지 않고 조건을 확인함", "상태를 감지하는 조건을 현재 코드에서 확인했습니다."
		case "provider_dependency":
			before = humanIdentifier(strings.TrimPrefix(fact.Object, "provider:"))
			after = "값 변경 없이 상태를 읽음"
			proof = "현재 코드에서 상태 참조를 확인했습니다."
		case "event_dispatch":
			before = "요청 전달 전 상태"
			after = humanIdentifier(strings.TrimPrefix(fact.Object, "event:")) + " 전달"
			proof = "요청 전달은 현재 코드에서 확인했습니다. 후속 상태는 다음 단계에서 확인합니다."
		}
	}
	return before, after, proof
}

type architectureNode struct {
	StepIndex  int
	Column     int
	Label      string
	Kind       string
	Status     flowir.Status
	Change     string
	StateAfter string
	Incoming   []string
	EditorURL  template.URL
}

type architectureLane struct {
	ID, Label string
	Nodes     []architectureNode
}

type architectureFlow struct {
	Columns int
	Lanes   []architectureLane
}

// architectureFlowView projects the same causal timeline into stable
// architectural lanes. It is deliberately derived from referenced facts—not
// path naming conventions—so selecting a node always selects the exact step
// and current source lens shown below.
func architectureFlowView(document flowir.Document, items []timelineItem) architectureFlow {
	facts := map[string]flowir.Fact{}
	for _, fact := range document.Facts {
		facts[fact.ID] = fact
	}
	lanes := []architectureLane{
		{ID: "interface", Label: "화면 · 사용자 결과"},
		{ID: "application", Label: "동작 · 제어 흐름"},
		{ID: "state", Label: "상태 · 값 변화"},
		{ID: "data", Label: "데이터 · 저장소"},
		{ID: "external", Label: "외부 시스템"},
	}
	index := map[string]int{"interface": 0, "application": 1, "state": 2, "data": 3, "external": 4}
	for stepIndex, item := range items {
		kind := architectureKind(item.Step, facts)
		incoming := make([]string, 0, len(item.Incoming))
		for _, edge := range item.Incoming {
			incoming = append(incoming, edge.Kind)
		}
		node := architectureNode{StepIndex: stepIndex, Column: stepIndex + 1, Label: item.Title, Kind: kind, Status: item.Step.Status, Change: item.ChangeLabel, StateAfter: item.StateAfter, Incoming: incoming, EditorURL: item.EditorURL}
		lanes[index[kind]].Nodes = append(lanes[index[kind]].Nodes, node)
	}
	visible := lanes[:0]
	for _, lane := range lanes {
		if len(lane.Nodes) > 0 {
			visible = append(visible, lane)
		}
	}
	return architectureFlow{Columns: len(items), Lanes: visible}
}

func architectureKind(step flowir.Step, facts map[string]flowir.Fact) string {
	ids := append(append([]string{}, step.BehaviorFacts...), step.ResultFacts...)
	kinds := map[string]bool{}
	for _, id := range ids {
		kinds[facts[id].Kind] = true
	}
	if kinds["external_call"] || kinds["external_result"] || kinds["external_boundary_unknown"] {
		return "external"
	}
	if kinds["repository_access"] {
		return "data"
	}
	if kinds["provider_dependency"] || kinds["notifier_operation"] || kinds["state_transition"] || kinds["notifier_state_transition"] || kinds["listener_condition"] || kinds["unknown_state"] {
		return "state"
	}
	if step.Actor == "user" || kinds["route_transition"] || kinds["visible_result"] {
		return "interface"
	}
	return "application"
}

type architecturePathItem struct{ ID, Label string }

func architecturePath(document flowir.Document) []architecturePathItem {
	seen := map[string]bool{}
	var out []architecturePathItem
	components := append(append([]string{}, document.Architecture.Components...), document.Architecture.Boundaries...)
	for _, component := range components {
		display := component
		switch {
		case component == "ui":
			display = "화면"
		case component == "application":
			display = "동작"
		case component == "state":
			display = "상태"
		case component == "data":
			display = "데이터"
		case component == "external":
			display = "외부 시스템"
		case strings.HasPrefix(component, "graph:"):
			display = "Dart 정적 분석"
		case strings.Contains(component, "::"):
			display = humanAction(component)
		case strings.HasPrefix(component, "provider:") || strings.HasPrefix(component, "state:"):
			display = humanIdentifier(component)
		case strings.Contains(component, "/"):
			display = path.Base(component)
		}
		if display != "" && !seen[display] {
			seen[display] = true
			out = append(out, architecturePathItem{ID: component, Label: display})
		}
	}
	return out
}

func resolvedDebt(reviews []store.DebtReview, document flowir.Document) []store.DebtReview {
	current := map[string]bool{}
	for _, unknown := range document.Unknowns {
		current[unknown.ID] = true
	}
	var out []store.DebtReview
	for _, review := range reviews {
		// A resolved review is useful only while the same evidence boundary still
		// exists in the current FlowIR. Once static analysis closes that boundary,
		// keeping its old internal question on screen adds cognitive debt back.
		if review.State == "resolved" && current[review.DebtID] {
			out = append(out, review)
		}
	}
	return out
}

type debtItem struct {
	ID, Reason, State string
	Title             string
	Confirmed         string
	Missing           string
	NextAction        string
	Location          string
	Cause             string
}

func actionableDebt(document flowir.Document) []debtItem {
	out := make([]debtItem, 0, len(document.Unknowns))
	results := observedResultNames(document)
	for _, debt := range document.Unknowns {
		item := debtItem{
			ID:         debt.ID,
			Reason:     debt.Reason,
			State:      debt.DebtState,
			Title:      "이 단계 이후의 동작이 타임라인 끝까지 연결되지 않았습니다.",
			Confirmed:  "FlowView에 표시된 단계까지는 현재 코드에서 확인했습니다.",
			Missing:    "그 다음 호출이나 사용자에게 보이는 결과를 현재 코드 근거로 확정하지 못했습니다.",
			NextAction: "아래 코드 위치에서 다음 호출 또는 종료 조건을 연결해야 합니다.",
			Location:   debtLocation(debt),
			Cause:      "현재 정적 분석 범위에서 다음 코드 연결을 찾지 못함",
		}
		switch debt.Reason {
		case "supported_user_action_missing":
			item.Cause = "화면 진입은 확인했지만 버튼·탭이 실행하는 메서드를 코드에서 연결하지 못함"
			item.Title = "화면 진입 다음의 사용자 동작이 아직 연결되지 않았습니다."
			item.Confirmed = "현재 화면 경로와 이 화면을 여는 코드는 확인했습니다."
			item.Missing = "버튼·탭 동작에서 다음 상태나 화면 결과로 이어지는 연결이 타임라인에 없습니다."
			item.NextAction = "버튼·탭의 onPressed/onTap이 실행하는 메서드와 그 뒤의 상태 변경 또는 화면 이동을 확인하면 됩니다."
		case "conditional_route_alternative":
			item.Cause = "조건문의 일부 경로에서 이어지는 다음 동작을 현재 코드만으로 확인하지 못함"
			item.Title = "조건의 다른 선택지가 다음 단계까지 연결되지 않았습니다."
			if len(results) > 0 {
				item.Confirmed = "현재 코드에서 " + strings.Join(results, ", ") + " 화면으로 이동하는 경로는 확인했습니다."
			}
			item.Missing = "조건이 맞지 않을 때 현재 화면에 머무는지, 다른 동작을 실행하는지 FlowView가 아직 구분하지 못합니다."
			item.NextAction = "표시된 조건문의 나머지 return/else 경로를 ‘화면 유지’, ‘다른 화면 이동’ 또는 ‘종료’ 단계로 연결하면 됩니다."
		case "unsupported_riverpod_pattern":
			item.Cause = "현재 분석기가 이 상태 관리 코드의 값 변경 방식을 끝까지 따라가지 못함"
			item.Title = "상태를 읽은 뒤 어떤 값이 바뀌는지 연결되지 않았습니다."
			item.Confirmed = "이 동작이 상태를 읽는 코드 위치까지는 확인했습니다."
			item.Missing = "호출되는 메서드, 대입되는 값, 그 값을 읽는 화면 중 하나 이상이 현재 타임라인에 없습니다."
			item.NextAction = "상태 읽기 → 메서드 호출 → 값 대입 → 화면 반영 순서로 코드를 연결하면 됩니다."
		case "dynamic_dispatch":
			item.Cause = "실행할 때 호출 대상이 선택되는 코드"
			item.Title = "실행할 때 결정되는 호출 대상이 다음 단계와 연결되지 않았습니다."
			item.Confirmed = "동적 호출이 시작되는 코드 위치까지는 확인했습니다."
			item.Missing = "실제로 선택되는 메서드나 화면을 정적 코드에서 하나로 결정할 수 없습니다."
			item.NextAction = "가능한 호출 대상을 명시하거나, 해당 조건을 검증하는 테스트를 연결하면 됩니다."
		case "EXTERNAL_BOUNDARY_UNKNOWN":
			item.Cause = "서버 응답 계약 또는 결과 처리 근거가 저장소에 없음"
			item.Title = "외부 호출 뒤 사용자에게 보이는 결과를 확인할 수 없습니다."
			item.Confirmed = "외부 호출이 시작되는 코드 위치까지는 확인했습니다."
			item.Missing = "성공·실패 응답이 상태와 화면에 어떻게 반영되는지 저장소 안에 근거가 없습니다."
			item.NextAction = "외부 응답 계약이나 결과 처리 테스트를 연결하면 됩니다."
		}
		out = append(out, item)
	}
	return out
}

func observedResultNames(document flowir.Document) []string {
	seen := map[string]bool{}
	var out []string
	for _, fact := range document.Facts {
		if (fact.Kind == "visible_result" || fact.Kind == "route_transition") && fact.Status == flowir.Observed && fact.Object != "" && !seen[fact.Object] {
			seen[fact.Object] = true
			out = append(out, fact.Object)
		}
	}
	return out
}

func debtLocation(debt flowir.UnknownDetail) string {
	if len(debt.Evidence) == 0 || debt.Evidence[0].Path == "" {
		return ""
	}
	location := debt.Evidence[0].Path
	if len(debt.Evidence[0].LineRange) > 0 && debt.Evidence[0].LineRange[0] > 0 {
		location += fmt.Sprintf(":%d", debt.Evidence[0].LineRange[0])
	}
	return location
}

//go:embed flowview.html
var flowViewSource string

var page = template.Must(template.New("flow").Parse(flowViewSource))
var exportPage = template.Must(template.New("export").Parse(staticFlowViewSource()))

func staticFlowViewSource() string {
	const pollingStart = "  const initialPublication = root.dataset.publication;"
	start := strings.Index(flowViewSource, pollingStart)
	if start < 0 {
		panic("FlowView export requires the runtime polling boundary")
	}
	end := strings.Index(flowViewSource[start:], "\n})();")
	if end < 0 {
		panic("FlowView export requires a closing script boundary")
	}
	// Keep the interaction script and its IIFE close, but remove the only
	// network activity. An exported report must render identically offline.
	return flowViewSource[:start] + flowViewSource[start+end:]
}

func (c *Core) view(w http.ResponseWriter, r *http.Request) {
	documents, publishedAt, status, err := c.store.GetBatch(r.Context())
	if err != nil {
		http.Error(w, "workspace unavailable", 500)
		return
	}
	workspace, err := buildWorkspace(documents)
	if err != nil {
		http.Error(w, "workspace invalid", 500)
		return
	}
	selected := r.URL.Query().Get("flow")
	if selected == "" {
		selected = documents[0].Current.ID
	}
	var document flowir.Document
	found := false
	for _, candidate := range documents {
		if candidate.Current.ID == selected {
			document, found = candidate, true
			break
		}
	}
	if !found {
		http.Error(w, "flow not found in current workspace", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	resolved := entrypoint.Result{Candidates: []entrypoint.EntryPoint{}}
	if selector := r.URL.Query().Get("selector"); selector != "" {
		resolved = entrypoint.Resolve(r.Context(), c.repo, selector, c.adapterCommand)
	}
	model, err := c.flowViewModel(r.Context(), document, status, workspace, publishedAt, r.URL.Query().Get("scenario"), resolved, false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := page.Execute(w, model); err != nil {
		http.Error(w, fmt.Sprintf("render flow: %v", err), 500)
	}
	if resolved.EntryPoint != nil {
		fmt.Fprintf(w, "<!-- %s — %s:%d -->", resolved.EntryPoint.FlowID, resolved.EntryPoint.Anchor.Path, resolved.EntryPoint.Anchor.LineRange[0])
	}
}

func (c *Core) flowViewModel(ctx context.Context, document flowir.Document, status string, workspace workspaceDocument, publishedAt, requestedScenario string, resolved entrypoint.Result, exported bool) (flowViewModel, error) {
	c.overlayMu.Lock()
	overlays := append([]ontology.Candidate(nil), c.overlays...)
	c.overlayMu.Unlock()
	c.domainMu.Lock()
	domainLabels, domainErr := ontology.LoadDomainLabels(c.repo)
	c.domainMu.Unlock()
	if domainErr != nil {
		return flowViewModel{}, fmt.Errorf("domain labels unavailable: %w", domainErr)
	}
	labelIndex := domainLabelMap(domainLabels)
	scenarios := buildScenarioNavigation(document, requestedScenario, labelIndex)
	if requestedScenario != "" && scenarios.Selected == nil {
		return flowViewModel{}, fmt.Errorf("scenario %q is not present in flow %q", requestedScenario, document.Current.ID)
	}
	if scenarios.Selected != nil {
		document, _ = scopeScenario(document, scenarios.Selected.ID)
	}
	reviews, _ := c.store.DebtReviews(ctx, document.Current.ID)
	states := map[string]string{}
	for _, review := range reviews {
		states[review.DebtID] = review.State
	}
	for i := range document.Unknowns {
		if state := states[document.Unknowns[i].ID]; state != "" {
			document.Unknowns[i].DebtState = state
		}
	}
	timelineItems := timelineWithDomainLabels(document, c.comparison, labelIndex)
	if exported {
		for i := range timelineItems {
			timelineItems[i].EditorURL = ""
		}
	}
	return flowViewModel{Document: document, Status: status, Resolved: resolved, Comparison: c.comparison, Overlays: overlays, Lenses: stepLenses(document), FactLabels: factLabels(document), Timeline: timelineItems, Architecture: architecturePath(document), ArchitectureFlow: architectureFlowView(document, timelineItems), Debt: actionableDebt(document), ResolvedDebt: resolvedDebt(reviews, document), Workspace: buildWorkspaceNavigation(workspace, document.Current.ID), Scenarios: scenarios, Publication: publishedAt + "|" + status, Export: exported}, nil
}
