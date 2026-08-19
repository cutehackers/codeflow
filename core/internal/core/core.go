// Package core starts the fixture-backed CodeFlow walking skeleton.
package core

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"path"
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
)

type Core struct {
	URL, Token     string
	server         *http.Server
	listener       net.Listener
	runtime        runtime.Handle
	store          *store.Store
	repo           string
	adapterCommand string
	flowID         string
	capture        func(string) (flowir.Basis, error)
	analysis       *AnalysisOptions
	reconcileMu    sync.Mutex
	reconcileTimer *time.Timer
	comparison     *compare.Result
	overlayMu      sync.Mutex
	overlays       []ontology.Candidate
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
	instance.server = &http.Server{Handler: instance.routes()}
	go instance.server.Serve(listener)
	return instance, nil
}

// AnalysisOptions describes the real CF-G05 public path. Unlike StartFixture,
// it accepts only an observed, current graph-and-Dart compiled document.
type AnalysisOptions struct{ Selector, CodeGraphURL, AdapterCommand string }

func StartAnalysis(ctx context.Context, repo string, options AnalysisOptions) (*Core, *compiler.Problem, error) {
	document, problem, err := compiler.Compile(ctx, compiler.Options{Repo: repo, Selector: options.Selector, CodeGraphURL: options.CodeGraphURL, AdapterCommand: options.AdapterCommand})
	if err != nil || problem != nil {
		return nil, problem, err
	}
	lock, err := runtime.Acquire(repo)
	if err != nil {
		return nil, nil, err
	}
	fail := func(e error) (*Core, *compiler.Problem, error) { lock.Release(); return nil, nil, e }
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
	instance := &Core{URL: "http://" + listener.Addr().String(), Token: token, listener: listener, runtime: lock, store: database, repo: document.Basis.Repository, adapterCommand: options.AdapterCommand, flowID: document.Current.ID, capture: manifest.Capture, analysis: &owned}
	if err = database.Publish(ctx, document, now, "ready"); err != nil {
		listener.Close()
		database.Close()
		return fail(err)
	}
	if err = lock.Write(listener.Addr().(*net.TCPAddr).Port, token, now); err != nil {
		listener.Close()
		database.Close()
		lock.Release()
		return nil, nil, err
	}
	instance.server = &http.Server{Handler: instance.routes()}
	go instance.server.Serve(listener)
	return instance, nil, nil
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
	if c.reconcileTimer != nil {
		c.reconcileTimer.Stop()
	}
	c.reconcileTimer = time.AfterFunc(100*time.Millisecond, func() { _ = c.Reconcile(context.Background()) })
}

// Refresh is the public missed-event recovery path. It deliberately executes
// the same authoritative reconciliation as a scheduled notification.
func (c *Core) Refresh(ctx context.Context) error { return c.Reconcile(ctx) }

func (c *Core) reconcileAnalysis(ctx context.Context) error {
	// Analysis now includes resolved Dart symbols and may take longer than a
	// notification debounce. Expose that work immediately while retaining the
	// last complete snapshot, instead of leaving stale content marked ready.
	if err := c.store.SetStatus(ctx, c.flowID, "analyzing"); err != nil {
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
		doc, problem, err := compiler.Compile(ctx, compiler.Options{Repo: c.repo, Selector: c.analysis.Selector, CodeGraphURL: c.analysis.CodeGraphURL, AdapterCommand: c.analysis.AdapterCommand, Basis: &basis})
		if err != nil || problem != nil {
			_ = c.store.SetStatus(ctx, c.flowID, "analyzing")
			if err != nil {
				return err
			}
			return nil
		}
		verified, err := c.capture(c.repo)
		if err == nil && verified.WorktreeFingerprint == basis.WorktreeFingerprint && verified.HeadRevision == basis.HeadRevision {
			return c.store.Publish(ctx, doc, time.Now().UTC().Format(time.RFC3339Nano), "ready")
		}
	}
	return c.store.SetStatus(ctx, c.flowID, "analyzing")
}
func (c *Core) Close(ctx context.Context) error {
	c.reconcileMu.Lock()
	if c.reconcileTimer != nil {
		c.reconcileTimer.Stop()
	}
	c.reconcileMu.Unlock()
	err := c.server.Shutdown(ctx)
	c.store.Close()
	c.runtime.Release()
	return err
}

// Document returns the same published document served from the authenticated
// API. It is used by the short-lived `codeflow analyze` public CLI path.
func (c *Core) Document(ctx context.Context) (flowir.Document, error) {
	d, _, _, err := c.store.Get(ctx, c.flowID)
	return d, err
}
func (c *Core) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", c.view)
	mux.HandleFunc("/api/v1/flows/", c.flow)
	mux.HandleFunc("/api/v1/compare", c.compare)
	mux.HandleFunc("/api/v1/entry-points/resolve", c.resolve)
	mux.HandleFunc("/api/v1/refresh", c.refresh)
	mux.HandleFunc("/api/v1/overlay", c.overlay)
	mux.HandleFunc("/api/v1/overlay/import", c.importOverlay)
	mux.HandleFunc("/api/v1/overlay/approve", c.approveOverlay)
	mux.HandleFunc("/api/v1/debt", c.debt)
	mux.HandleFunc("/api/v1/debt/review", c.reviewDebt)
	return mux
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
	reviews, err := c.store.DebtReviews(r.Context(), c.flowID)
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
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&request); err != nil || request.ID == "" {
		writeJSON(w, http.StatusBadRequest, envelope{Status: "unknown", Unknowns: []any{}, Error: &apiError{Code: "DEBT_REVIEW_MALFORMED", Message: "id and state are required"}})
		return
	}
	if err := c.store.ReviewDebt(r.Context(), c.flowID, request.ID, request.State, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		code, status := "DEBT_REVIEW_INVALID", http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			code, status = "DEBT_NOT_FOUND", http.StatusNotFound
		}
		writeJSON(w, status, envelope{Status: "unknown", Unknowns: []any{}, Error: &apiError{Code: code, Message: err.Error()}})
		return
	}
	reviews, _ := c.store.DebtReviews(r.Context(), c.flowID)
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
	doc, _, status, err := c.store.Get(r.Context(), c.flowID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, envelope{Status: "unavailable", Unknowns: []any{}, Error: &apiError{Code: "STORAGE_ERROR", Message: err.Error()}})
		return
	}
	unknowns := make([]any, len(doc.Unknowns))
	for i := range doc.Unknowns {
		unknowns[i] = doc.Unknowns[i]
	}
	writeJSON(w, http.StatusOK, envelope{Basis: &doc.Basis, Status: status, Data: doc, Unknowns: unknowns, ViewURL: c.URL + "/"})
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
	return r.Header.Get("X-CodeFlow-Token") == c.Token || strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") == c.Token
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
	unknowns := make([]any, len(document.Unknowns))
	for i := range document.Unknowns {
		unknowns[i] = document.Unknowns[i]
	}
	reviews, _ := c.store.DebtReviews(r.Context(), document.Current.ID)
	writeJSON(w, http.StatusOK, envelope{Basis: &document.Basis, Status: status, Data: document, Unknowns: unknowns, Reviews: reviews, Lenses: documentLenses(document), ViewURL: viewURL, Metadata: metadata{GeneratedAt: generated, RuntimeStatus: status, ViewURL: viewURL}})
}
func writeJSON(w http.ResponseWriter, status int, body envelope) {
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
	case "user_action":
		return "사용자 동작 · " + shortSymbol(value)
	case "condition", "confirmation_condition":
		return "조건 확인 · " + shortSymbol(value)
	case "provider_dependency":
		return "상태 의존성 · " + shortSymbol(value)
	case "unknown_state":
		return "상태 변경 미확정 · " + shortSymbol(value)
	case "state_transition", "notifier_state_transition":
		return "상태 변경 · " + shortSymbol(value)
	case "event_dispatch":
		return "이벤트 전달 · " + shortSymbol(value)
	case "terminal_result":
		return "화면 이동 없이 현재 동작 종료"
	case "listener_condition":
		return "상태 감지 · " + shortSymbol(value)
	case "route_transition", "visible_result":
		return "화면 결과 · " + strings.TrimPrefix(value, "route:")
	case "repository_access":
		return "저장소 접근 · " + shortSymbol(value)
	case "external_call", "external_result":
		return "외부 경계 · " + shortSymbol(value)
	default:
		return fact.Kind + " · " + shortSymbol(value)
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

type timelineItem struct {
	Step           flowir.Step
	Title          string
	StateDelta     string
	StateBefore    string
	StateAfter     string
	StateProof     string
	CodeImpact     string
	ResultImpact   string
	HasStateChange bool
	Change         string
	ChangeLabel    string
	SourceState    string
	Lens           lens.Source
	EditorURL      template.URL
	Incoming       []causalView
	Outgoing       []causalView
	Branches       []branchView
	TrustLabel     string
	BranchPath     string
	BreakAfter     bool
	Alternative    bool
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

func timeline(document flowir.Document, comparison *compare.Result) []timelineItem {
	labels := factLabels(document)
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
		title := "미확정 결과"
		ids := append(append([]string{}, step.BehaviorFacts...), step.ResultFacts...)
		if len(ids) > 0 {
			for _, fact := range document.Facts {
				if fact.ID == ids[len(ids)-1] {
					title = displayFact(fact)
					break
				}
			}
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
		item := timelineItem{Step: step, Title: title, Change: change, ChangeLabel: displayChange(change), SourceState: sourceState, Lens: source, TrustLabel: trustLabel(step.Status)}
		if source.Status == "ready" && source.EditorURL != "" {
			item.EditorURL = template.URL(source.EditorURL)
		}
		for _, edge := range document.CausalEdges {
			fromCurrent, toCurrent := facts[edge.FromFact], facts[edge.ToFact]
			if toCurrent && !fromCurrent {
				item.Incoming = append(item.Incoming, causalView{Kind: displayCausalKind(edge.Kind), RawKind: edge.Kind, Label: labels[edge.FromFact], Status: edge.Status})
			}
			if fromCurrent && !toCurrent {
				item.Outgoing = append(item.Outgoing, causalView{Kind: displayCausalKind(edge.Kind), RawKind: edge.Kind, Label: labels[edge.ToFact], Status: edge.Status})
			}
		}
		item.StateDelta = title
		item.StateBefore, item.StateAfter, item.StateProof = stepStateDelta(step, facts, document.Facts)
		item.HasStateChange = stepChangesState(facts, document.Facts)
		if comparison != nil {
			item.CodeImpact = item.ChangeLabel
		} else if sourceState == "added" || sourceState == "modified" || sourceState == "renamed" || sourceState == "untracked" {
			item.CodeImpact = sourceState + " · baseline 미선택"
		} else {
			item.CodeImpact = "현재 코드 · baseline 미선택"
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
			view := branchView{ID: branch.ID, Condition: labels[branch.ConditionFact], Status: branch.Status}
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
			return "이전 값 미확정", strings.TrimPrefix(fact.Object, "state:"), "대입 결과는 observed · 실행 직전 값은 unknown"
		case "listener_condition":
			return strings.TrimPrefix(fact.Object, "state."), "값 변경 없음 · 조건이 관찰", "resolved listener condition"
		case "provider_dependency":
			before = strings.TrimPrefix(fact.Object, "provider:")
			after = "값 변경 없음 · provider 참조"
			proof = "resolved provider dependency"
		case "event_dispatch":
			before = "dispatch 이전 상태"
			after = strings.TrimPrefix(fact.Object, "event:") + " 전달"
			proof = "event 전달 observed · 후속 상태는 다음 단계에서 판정"
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
		{ID: "interface", Label: "UI · 사용자 결과"},
		{ID: "application", Label: "Application · 제어"},
		{ID: "state", Label: "State · 상태 전이"},
		{ID: "data", Label: "Data · 저장소"},
		{ID: "external", Label: "External · 외부 경계"},
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
	return architectureFlow{Columns: len(items), Lanes: lanes}
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

func architecturePath(document flowir.Document) []string {
	seen := map[string]bool{}
	var out []string
	components := append(append([]string{}, document.Architecture.Components...), document.Architecture.Boundaries...)
	for _, component := range components {
		display := component
		if strings.Contains(component, "::") || strings.HasPrefix(component, "provider:") || strings.HasPrefix(component, "state:") {
			display = shortSymbol(component)
		} else if strings.Contains(component, "/") {
			display = path.Base(component)
		}
		if display != "" && !seen[display] {
			seen[display] = true
			out = append(out, display)
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
		case "conditional_route_alternative":
			item.Cause = "조건문의 일부 경로가 정적 코드 흐름에 아직 연결되지 않음"
			item.Title = "조건의 다른 선택지가 다음 단계까지 연결되지 않았습니다."
			if len(results) > 0 {
				item.Confirmed = "현재 코드에서 " + strings.Join(results, ", ") + " 화면으로 이동하는 경로는 확인했습니다."
			}
			item.Missing = "조건이 맞지 않을 때 현재 화면에 머무는지, 다른 동작을 실행하는지 FlowView가 아직 구분하지 못합니다."
			item.NextAction = "표시된 조건문의 나머지 return/else 경로를 ‘화면 유지’, ‘다른 화면 이동’ 또는 ‘종료’ 단계로 연결하면 됩니다."
		case "unsupported_riverpod_pattern":
			item.Cause = "현재 Riverpod 분석 규칙이 이 상태 변경 형태를 지원하지 않음"
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

func (c *Core) view(w http.ResponseWriter, r *http.Request) {
	document, _, status, err := c.store.Get(r.Context(), c.flowID)
	if err != nil {
		http.Error(w, "flow unavailable", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	resolved := entrypoint.Result{Candidates: []entrypoint.EntryPoint{}}
	if selector := r.URL.Query().Get("selector"); selector != "" {
		resolved = entrypoint.Resolve(r.Context(), c.repo, selector, c.adapterCommand)
	}
	c.overlayMu.Lock()
	overlays := append([]ontology.Candidate(nil), c.overlays...)
	c.overlayMu.Unlock()
	reviews, _ := c.store.DebtReviews(r.Context(), document.Current.ID)
	states := map[string]string{}
	for _, review := range reviews {
		states[review.DebtID] = review.State
	}
	for i := range document.Unknowns {
		if state := states[document.Unknowns[i].ID]; state != "" {
			document.Unknowns[i].DebtState = state
		}
	}
	timelineItems := timeline(document, c.comparison)
	if err := page.Execute(w, struct {
		Document         flowir.Document
		Status           string
		Resolved         entrypoint.Result
		Comparison       *compare.Result
		Overlays         []ontology.Candidate
		Lenses           map[string]lens.Source
		FactLabels       map[string]string
		Timeline         []timelineItem
		Architecture     []string
		ArchitectureFlow architectureFlow
		Debt             []debtItem
		ResolvedDebt     []store.DebtReview
	}{document, status, resolved, c.comparison, overlays, stepLenses(document), factLabels(document), timelineItems, architecturePath(document), architectureFlowView(document, timelineItems), actionableDebt(document), resolvedDebt(reviews, document)}); err != nil {
		http.Error(w, fmt.Sprintf("render flow: %v", err), 500)
	}
	if resolved.EntryPoint != nil {
		fmt.Fprintf(w, "<!-- %s — %s:%d -->", resolved.EntryPoint.FlowID, resolved.EntryPoint.Anchor.Path, resolved.EntryPoint.Anchor.LineRange[0])
	}
}
