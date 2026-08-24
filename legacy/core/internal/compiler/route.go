// Package compiler converts validated graph and Dart evidence into the small
// causal route flow supported by CF-G05.
package compiler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"codeflow/core/internal/codegraph"
	"codeflow/core/internal/dartadapter"
	"codeflow/core/internal/entrypoint"
	"codeflow/core/internal/flowir"
	"codeflow/core/internal/manifest"
)

type Options struct {
	Repo, Selector, CodeGraphURL, AdapterCommand string
	// Basis is supplied only by the immutable baseline mirror. Its files are
	// still re-hashed below before graph evidence can enter FlowIR.
	Basis *flowir.Basis
}
type Problem struct{ Code, Message string }

// Session owns one initialized Dart Analyzer process and serializes requests
// against it. A persistent Core reuses this session across reconciliations so
// package resolution and analyzer contexts are not rebuilt for every edit.
type Session struct {
	options Options
	client  *dartadapter.Client
	graph   *codegraph.Client
	mu      sync.Mutex
	closed  bool
}

func NewSession(ctx context.Context, opt Options) (*Session, *Problem, error) {
	client, err := dartadapter.Start(ctx, opt.AdapterCommand)
	if err != nil {
		failure := dartadapter.AsFailure(err)
		return nil, &Problem{failure.Code, failure.Message}, nil
	}
	if err = client.Initialize(ctx); err != nil {
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = client.Shutdown(shutdown)
		failure := dartadapter.AsFailure(err)
		return nil, &Problem{failure.Code, failure.Message}, nil
	}
	return &Session{options: opt, client: client, graph: codegraph.New(opt.CodeGraphURL)}, nil, nil
}

func (s *Session) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.client.Shutdown(ctx)
}

// Compile is fail-closed: graph relations and semantic anchors must describe
// the exact captured worktree. No source-derived relationship is accepted
// without a current graph slice.
func Compile(ctx context.Context, opt Options) (flowir.Document, *Problem, error) {
	documents, problem, err := CompileMany(ctx, opt, []string{opt.Selector})
	if err != nil || problem != nil {
		return flowir.Document{}, problem, err
	}
	return documents[0], nil, nil
}

// CompileMany compiles a bounded set of independently valid FlowIR documents
// against one immutable Basis and one initialized Dart analyzer process. It is
// all-or-nothing: a caller may publish the returned slice as one batch without
// mixing worktree observations or partially successful selectors.
func CompileMany(ctx context.Context, opt Options, selectors []string) ([]flowir.Document, *Problem, error) {
	selectors, validationProblem := validateSelectors(selectors)
	if validationProblem != nil {
		return nil, validationProblem, nil
	}
	session, problem, err := NewSession(ctx, opt)
	if err != nil || problem != nil {
		return nil, problem, err
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = session.Close(shutdown)
	}()
	return session.CompileMany(ctx, selectors, opt.Basis)
}

func (s *Session) CompileMany(ctx context.Context, selectors []string, suppliedBasis *flowir.Basis) ([]flowir.Document, *Problem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, &Problem{"ADAPTER_UNAVAILABLE", "Dart analyzer session is closed"}, nil
	}
	selectors, problem := validateSelectors(selectors)
	if problem != nil {
		return nil, problem, nil
	}
	basis := flowir.Basis{}
	var err error
	if suppliedBasis != nil {
		basis = *suppliedBasis
	} else if basis, err = manifest.Capture(s.options.Repo); err != nil {
		return nil, nil, err
	}
	entries, err := s.client.Discover(ctx, basis.Repository)
	if err != nil {
		failure := dartadapter.AsFailure(err)
		return nil, &Problem{failure.Code, failure.Message}, nil
	}
	type target struct {
		selector string
		entry    entrypoint.EntryPoint
		paths    []string
	}
	targets := make([]target, 0, len(selectors))
	analysisPathSet := map[string]bool{}
	for _, selector := range selectors {
		resolved := entrypoint.ResolveDiscovered(basis.Repository, selector, entries, basis)
		if resolved.State != entrypoint.Ready {
			if resolved.Unknown != nil {
				return nil, &Problem{resolved.Unknown.Code, selectorProblem(selector, resolved.Unknown.Message)}, nil
			}
			return nil, &Problem{"ENTRY_POINT_UNKNOWN", selectorProblem(selector, "entry point is unavailable")}, nil
		}
		var paths []string
		if strings.HasPrefix(resolved.EntryPoint.FlowID, "system:") {
			// A system callback's entry anchor is itself direct current-source
			// evidence. Its semantic facts remain Analyzer-resolved; unlike a
			// route it has no GoRoute structural relationship to ask from the
			// fallback graph bridge.
			paths = []string{resolved.EntryPoint.Anchor.Path}
		} else {
			rels, err := s.graph.Relationships(ctx, basis.Repository, resolved.EntryPoint.FlowID)
			if err != nil {
				f := asGraph(err)
				return nil, &Problem{f.Code, selectorProblem(selector, f.Message)}, nil
			}
			var validationErr error
			paths, validationErr = validateRelationships(basis, rels)
			if validationErr != nil {
				return nil, &Problem{"STALE_GRAPH", selectorProblem(selector, validationErr.Error())}, nil
			}
		}
		for _, path := range paths {
			analysisPathSet[path] = true
		}
		targets = append(targets, target{selector: selector, entry: *resolved.EntryPoint, paths: paths})
	}
	analysisPaths := make([]string, 0, len(analysisPathSet))
	for path := range analysisPathSet {
		analysisPaths = append(analysisPaths, path)
	}
	sort.Strings(analysisPaths)
	documents := make([]flowir.Document, 0, len(targets))
	for _, target := range targets {
		document, problem, err := compileResolved(ctx, basis, target.entry, target.paths, analysisPaths, s.graph, s.client)
		if err != nil || problem != nil {
			if problem != nil {
				problem.Message = selectorProblem(target.selector, problem.Message)
			}
			return nil, problem, err
		}
		documents = append(documents, document)
	}
	return documents, nil, nil
}

func compileResolved(ctx context.Context, basis flowir.Basis, resolved entrypoint.EntryPoint, paths, analysisPaths []string, graph *codegraph.Client, client *dartadapter.Client) (flowir.Document, *Problem, error) {
	semantic, err := client.RefineRouteFlowWithAnalysisPaths(ctx, basis.Repository, resolved.FlowID, paths, analysisPaths)
	if err != nil {
		f := dartadapter.AsFailure(err)
		return flowir.Document{}, &Problem{f.Code, f.Message}, nil
	}
	facts, err := validateSemantic(basis, semantic)
	if err != nil {
		return flowir.Document{}, &Problem{"STALE_GRAPH", err.Error()}, nil
	}
	doc, err := assemble(basis, resolved, facts, graph.Backend)
	if err != nil {
		return flowir.Document{}, nil, err
	}
	// A route can expose several independently selectable actions. The
	// deterministic scenario projection preserves that separation without
	// changing any fact, causal edge, or behavior comparison identity.
	flowir.DeriveScenarios(&doc)
	if err := flowir.Validate(doc); err != nil {
		return flowir.Document{}, nil, err
	}
	return doc, nil, nil
}

func validateSelectors(values []string) ([]string, *Problem) {
	if len(values) == 0 {
		values = []string{""}
	}
	if len(values) > 3 {
		return nil, &Problem{"FLOW_SET_TOO_LARGE", "at most three selectors are supported by the local multi-flow workspace"}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" && len(values) > 1 {
			return nil, &Problem{"SELECTOR_REQUIRED", "every selector in a multi-flow request must be non-empty"}
		}
		if seen[value] {
			return nil, &Problem{"DUPLICATE_SELECTOR", selectorProblem(value, "the selector appears more than once")}
		}
		seen[value] = true
		out = append(out, value)
	}
	return out, nil
}

func selectorProblem(selector, message string) string {
	if selector == "" {
		return message
	}
	return selector + ": " + message
}
func asGraph(err error) *codegraph.Failure {
	if f, ok := err.(*codegraph.Failure); ok {
		return f
	}
	return &codegraph.Failure{Code: "CODEGRAPH_UNAVAILABLE", Message: err.Error()}
}
func validateRelationships(basis flowir.Basis, rels []codegraph.Relationship) ([]string, error) {
	m := map[string]flowir.ManifestEntry{}
	for _, e := range basis.Manifest {
		m[e.Path] = e
	}
	paths := map[string]bool{}
	for _, r := range rels {
		if r.Kind != "call" {
			continue
		}
		for _, a := range []codegraph.Anchor{r.From, r.To} {
			e, ok := m[a.Path]
			if !ok || e.Type != "file" || a.Symbol == "" || a.FileHash == "" || a.FileHash != e.FileHash || a.Revision == "" || a.Revision != basis.HeadRevision || a.ByteStart < 0 || a.ByteEnd <= a.ByteStart {
				return nil, fmt.Errorf("graph relationship anchor %s is stale or incomplete", a.Path)
			}
			b, err := os.ReadFile(filepath.Join(basis.Repository, filepath.FromSlash(a.Path)))
			if err != nil || a.ByteEnd > len(b) || flowir.SHA256Bytes(b) != a.FileHash {
				return nil, fmt.Errorf("graph relationship anchor %s no longer matches source", a.Path)
			}
			paths[a.Path] = true
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("graph slice contains no validated call relationship")
	}
	out := make([]string, 0, len(paths))
	for p := range paths {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

type semanticFact struct {
	kind, subject, object string
	proof, symbolID       string
	anchor                flowir.Anchor
	status                flowir.Status
}

func validateSemantic(basis flowir.Basis, values []dartadapter.SemanticFact) ([]semanticFact, error) {
	m := map[string]flowir.ManifestEntry{}
	for _, e := range basis.Manifest {
		m[e.Path] = e
	}
	out := make([]semanticFact, 0, len(values))
	for _, v := range values {
		e, ok := m[v.Anchor.Path]
		if !ok || e.Type != "file" || v.Anchor.ByteStart < 0 || v.Anchor.ByteEnd <= v.Anchor.ByteStart {
			return nil, fmt.Errorf("semantic anchor %s is invalid", v.Anchor.Path)
		}
		b, err := os.ReadFile(filepath.Join(basis.Repository, filepath.FromSlash(v.Anchor.Path)))
		if err != nil || v.Anchor.ByteEnd > len(b) || flowir.SHA256Bytes(b) != e.FileHash {
			return nil, fmt.Errorf("semantic anchor %s is stale", v.Anchor.Path)
		}
		lineEnd := v.Anchor.LineEnd
		if lineEnd < v.Anchor.LineStart {
			lineEnd = v.Anchor.LineStart
		}
		a := flowir.Anchor{Kind: "code", Path: v.Anchor.Path, Symbol: symbolFor(v.Kind), LineRange: []int{v.Anchor.LineStart, lineEnd}, ByteRange: []int{v.Anchor.ByteStart, v.Anchor.ByteEnd}, FileHash: e.FileHash, SpanHash: flowir.SHA256Bytes(b[v.Anchor.ByteStart:v.Anchor.ByteEnd]), Fingerprint: v.Anchor.Fingerprint, Revision: basis.HeadRevision}
		status := flowir.Observed
		if v.Kind == "dynamic_dispatch" || v.Kind == "unknown_state" || v.Kind == "external_boundary_unknown" {
			status = flowir.Unknown
		}
		out = append(out, semanticFact{kind: v.Kind, subject: v.Subject, object: v.Object, proof: v.Proof, symbolID: v.SymbolID, anchor: a, status: status})
	}
	return out, nil
}
func symbolFor(kind string) string {
	if kind == "route_transition" {
		return "route"
	}
	if kind == "state_transition" || kind == "unknown_state" {
		return "ui_state"
	}
	return "code"
}
func insertTimelineStep(steps []flowir.Step, next flowir.Step) []flowir.Step {
	for i := range steps {
		if steps[i].Order >= next.Order {
			steps[i].Order++
		}
	}
	steps = append(steps, next)
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].Order < steps[j].Order })
	return steps
}

// transitionsForAction follows only resolved call facts starting at the
// selected action. A route transition elsewhere in the same source slice is
// not evidence that this action causes it. The returned path is the shortest
// deterministic call path to the sole transition, when one exists.
func transitionsForAction(action *flowir.Fact, calls, transitions []*flowir.Fact) ([]*flowir.Fact, []*flowir.Fact) {
	type reach struct {
		subject string
		path    []*flowir.Fact
	}
	orderedCalls := append([]*flowir.Fact(nil), calls...)
	sort.Slice(orderedCalls, func(i, j int) bool { return orderedCalls[i].ID < orderedCalls[j].ID })
	queue := []reach{{subject: action.Subject}}
	paths := map[string][]*flowir.Fact{action.Subject: nil}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range orderedCalls {
			if edge.Subject != current.subject || edge.Object == "" {
				continue
			}
			if _, seen := paths[edge.Object]; seen {
				continue
			}
			path := append(append([]*flowir.Fact(nil), current.path...), edge)
			paths[edge.Object] = path
			queue = append(queue, reach{subject: edge.Object, path: path})
		}
	}
	matched := make([]*flowir.Fact, 0)
	for _, candidate := range transitions {
		if _, ok := paths[candidate.Subject]; ok {
			matched = append(matched, candidate)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })
	if len(matched) != 1 {
		return matched, nil
	}
	return matched, paths[matched[0].Subject]
}

// assembleActionSet preserves every resolved action on a screen. The order is
// a deterministic presentation order; causal edges still make it explicit
// that every action starts at the entry point rather than following the prior
// action. An unsupported or ambiguous outcome remains an inline unknown.
func assembleActionSet(basis flowir.Basis, entry entrypoint.EntryPoint, entryFact flowir.Fact, facts []flowir.Fact, actions, calls, transitions []*flowir.Fact, backend string) (flowir.Document, error) {
	sort.Slice(actions, func(i, j int) bool {
		left, right := actions[i].Evidence[0], actions[j].Evidence[0]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if len(left.ByteRange) == 2 && len(right.ByteRange) == 2 && left.ByteRange[0] != right.ByteRange[0] {
			return left.ByteRange[0] < right.ByteRange[0]
		}
		return actions[i].ID < actions[j].ID
	})
	steps := make([]flowir.Step, 0, len(actions)*2)
	unknowns := []flowir.UnknownDetail{}
	components := []string{entry.Anchor.Path}
	visibleIDs := map[string]bool{}
	knownActions := 0
	order := 1
	for _, current := range actions {
		actionStep := flowir.Step{ID: flowir.Hash(entry.FlowID, entryFact.ID, current.ID), BehaviorKey: entry.FlowID + ":user:" + current.Subject, Order: order, Actor: "user", TriggerFact: entryFact.ID, BehaviorFacts: []string{current.ID}, PrimaryEvidence: []flowir.Anchor{current.Evidence[0]}, Status: flowir.Observed}
		steps = append(steps, actionStep)
		components = append(components, current.Subject)
		order++
		matched, path := transitionsForAction(current, calls, transitions)
		if len(matched) != 1 {
			reason := "missing_relation"
			question := "Which visible route or UI result follows this action?"
			if len(matched) > 1 {
				reason = "ambiguous_route_transition"
				question = "Which observed route transition is the result of this action?"
			}
			unknownStep := flowir.Step{ID: flowir.Hash(entry.FlowID, current.ID, reason), BehaviorKey: entry.FlowID + ":system:" + reason + ":" + current.Subject, Order: order, Actor: "system", TriggerFact: current.ID, PrimaryEvidence: []flowir.Anchor{current.Evidence[0]}, Status: flowir.Unknown}
			steps = append(steps, unknownStep)
			unknowns = append(unknowns, flowir.UnknownDetail{ID: flowir.Hash("unknown", reason, current.ID), Question: question, Reason: reason, RelatedSteps: []string{unknownStep.ID}, Evidence: []flowir.Anchor{current.Evidence[0]}})
			order++
			continue
		}
		knownActions++
		transition := matched[0]
		visible := flowir.Fact{ID: flowir.Hash(flowir.SchemaVersion, "visible_result", transition.Subject, transition.Object, transition.Evidence[0].Fingerprint), Kind: "visible_result", Subject: transition.Subject, Object: transition.Object, Proof: transition.Proof, SymbolID: transition.SymbolID, Evidence: transition.Evidence, Status: flowir.Observed}
		if !visibleIDs[visible.ID] {
			facts = append(facts, visible)
			visibleIDs[visible.ID] = true
		}
		behavior := make([]string, 0, len(path)+1)
		anchors := make([]flowir.Anchor, 0, 3)
		for _, call := range path {
			behavior = append(behavior, call.ID)
			if len(anchors) < 2 {
				anchors = append(anchors, call.Evidence[0])
			}
		}
		behavior = append(behavior, transition.ID)
		anchors = append(anchors, transition.Evidence[0])
		if len(anchors) > 3 {
			anchors = anchors[len(anchors)-3:]
		}
		resultStep := flowir.Step{ID: flowir.Hash(entry.FlowID, current.ID, transition.ID), BehaviorKey: entry.FlowID + ":system:route_transition:" + current.Subject + ":" + transition.Object, Order: order, Actor: "system", TriggerFact: current.ID, BehaviorFacts: behavior, ResultFacts: []string{visible.ID}, PrimaryEvidence: anchors, Status: flowir.Observed}
		steps = append(steps, resultStep)
		components = append(components, transition.Subject)
		order++
	}
	status := flowir.Observed
	if len(unknowns) > 0 {
		status = flowir.Mixed
		if knownActions == 0 {
			status = flowir.Unknown
		}
	}
	doc := flowir.Document{SchemaVersion: flowir.SchemaVersion, Basis: basis, Facts: facts, Architecture: flowir.ArchitectureSlice{EntryPoints: []string{entry.FlowID}, Boundaries: []string{"ui", "application", "graph:" + backend}, Components: components, Relations: []string{"call", "transition"}}, Current: flowir.Flow{ID: entry.FlowID, FlowKey: entry.FlowID, EntryPointFact: entryFact.ID, Steps: steps, Status: status}, Unknowns: unknowns}
	doc.CausalEdges = causalEdges(doc)
	attachDebtGuidance(&doc)
	return doc, flowir.Validate(doc)
}

func assemble(basis flowir.Basis, entry entrypoint.EntryPoint, values []semanticFact, backend string) (flowir.Document, error) {
	entryFact := flowir.Fact{ID: flowir.Hash(flowir.SchemaVersion, "entry_point", entry.FlowID, "", entry.Anchor.Fingerprint), Kind: "entry_point", Subject: entry.FlowID, Proof: "framework_rule_v1", Evidence: []flowir.Anchor{entry.Anchor}, Status: flowir.Observed}
	facts := []flowir.Fact{entryFact}
	var action, systemEvent, call, transition, condition, dynamic, dependency, operation, state, unknownState, repository, external, externalResult, externalUnknown, confirmation, terminalResult, eventDispatch, notifierState, listenerCondition, listenerRoute *flowir.Fact
	actions := []*flowir.Fact{}
	calls := []*flowir.Fact{}
	transitions := []*flowir.Fact{}
	for _, v := range values {
		kind := v.kind
		if kind != "user_action" && kind != "system_event" && kind != "call" && kind != "route_transition" && kind != "condition" && kind != "dynamic_dispatch" && kind != "provider_dependency" && kind != "notifier_operation" && kind != "state_transition" && kind != "unknown_state" && kind != "repository_access" && kind != "external_call" && kind != "external_result" && kind != "external_boundary_unknown" && kind != "confirmation_condition" && kind != "terminal_result" && kind != "event_dispatch" && kind != "notifier_state_transition" && kind != "listener_condition" {
			continue
		}
		f := flowir.Fact{ID: flowir.Hash(flowir.SchemaVersion, kind, v.subject, v.object, v.anchor.Fingerprint), Kind: kind, Subject: v.subject, Object: v.object, Proof: v.proof, SymbolID: v.symbolID, Evidence: []flowir.Anchor{v.anchor}, Status: v.status}
		facts = append(facts, f)
		switch kind {
		case "user_action":
			actions = append(actions, &facts[len(facts)-1])
			if action == nil {
				action = &facts[len(facts)-1]
			}
		case "system_event":
			if systemEvent == nil {
				systemEvent = &facts[len(facts)-1]
			}
		case "call":
			calls = append(calls, &facts[len(facts)-1])
		case "route_transition":
			transitions = append(transitions, &facts[len(facts)-1])
		case "condition":
			if condition == nil {
				condition = &facts[len(facts)-1]
			}
		case "dynamic_dispatch":
			if dynamic == nil {
				dynamic = &facts[len(facts)-1]
			}
		case "provider_dependency":
			if dependency == nil {
				dependency = &facts[len(facts)-1]
			}
		case "notifier_operation":
			if operation == nil {
				operation = &facts[len(facts)-1]
			}
		case "state_transition":
			if state == nil {
				state = &facts[len(facts)-1]
			}
		case "unknown_state":
			if unknownState == nil {
				unknownState = &facts[len(facts)-1]
			}
		case "repository_access":
			if repository == nil {
				repository = &facts[len(facts)-1]
			}
		case "external_call":
			if external == nil {
				external = &facts[len(facts)-1]
			}
		case "external_result":
			if externalResult == nil {
				externalResult = &facts[len(facts)-1]
			}
		case "external_boundary_unknown":
			if externalUnknown == nil {
				externalUnknown = &facts[len(facts)-1]
			}
		case "confirmation_condition":
			if confirmation == nil {
				confirmation = &facts[len(facts)-1]
			}
		case "terminal_result":
			if terminalResult == nil {
				terminalResult = &facts[len(facts)-1]
			}
		case "event_dispatch":
			if eventDispatch == nil {
				eventDispatch = &facts[len(facts)-1]
			}
		case "notifier_state_transition":
			if notifierState == nil {
				notifierState = &facts[len(facts)-1]
			}
		case "listener_condition":
			if listenerCondition == nil {
				listenerCondition = &facts[len(facts)-1]
			}
		}
	}
	if listenerCondition != nil {
		var listenerTransitions []*flowir.Fact
		for _, candidate := range transitions {
			if candidate.Subject == listenerCondition.Subject {
				listenerTransitions = append(listenerTransitions, candidate)
			}
		}
		if len(listenerTransitions) == 1 {
			listenerRoute = listenerTransitions[0]
		}
	}
	if action == nil && systemEvent != nil {
		action = systemEvent
	}
	actionActor := "user"
	if systemEvent != nil && action == systemEvent {
		actionActor = "system"
	}
	if action == nil {
		entryView := flowir.Fact{ID: flowir.Hash(flowir.SchemaVersion, "screen_entry", entry.FlowID, entry.Anchor.Fingerprint), Kind: "screen_entry", Subject: entry.FlowID, Proof: "framework_rule_v1", Evidence: []flowir.Anchor{entry.Anchor}, Status: flowir.Observed}
		facts = append(facts, entryView)
		step := flowir.Step{ID: flowir.Hash(entry.FlowID, entryFact.ID, entryView.ID), BehaviorKey: entry.FlowID + ":system:screen_entry", Order: 1, Actor: "system", TriggerFact: entryFact.ID, BehaviorFacts: []string{entryView.ID}, PrimaryEvidence: []flowir.Anchor{entry.Anchor}, Status: flowir.Unknown}
		doc := flowir.Document{SchemaVersion: flowir.SchemaVersion, Basis: basis, Facts: facts, Architecture: flowir.ArchitectureSlice{EntryPoints: []string{entry.FlowID}, Boundaries: []string{"ui", "graph:" + backend}, Components: []string{entry.Anchor.Path, entry.FlowID}, Relations: []string{"screen_entry"}}, Current: flowir.Flow{ID: entry.FlowID, FlowKey: entry.FlowID, EntryPointFact: entryFact.ID, Steps: []flowir.Step{step}, Status: flowir.Unknown}, Unknowns: []flowir.UnknownDetail{{ID: flowir.Hash("unknown", "supported_user_action_missing", entry.FlowID), Question: "Which user action continues this screen flow?", Reason: "supported_user_action_missing", RelatedSteps: []string{step.ID}, Evidence: []flowir.Anchor{entry.Anchor}}}}
		doc.CausalEdges = causalEdges(doc)
		attachDebtGuidance(&doc)
		return doc, flowir.Validate(doc)
	}
	if len(actions) > 1 {
		return assembleActionSet(basis, entry, entryFact, facts, actions, calls, transitions, backend)
	}
	matched, path := transitionsForAction(action, calls, transitions)
	if len(matched) == 1 {
		transition = matched[0]
		if len(path) > 0 {
			call = path[0]
		}
	} else if len(matched) > 1 {
		step := flowir.Step{ID: flowir.Hash(entry.FlowID, entryFact.ID, action.ID), BehaviorKey: entry.FlowID + ":entry:" + action.Subject, Order: 1, Actor: actionActor, TriggerFact: entryFact.ID, BehaviorFacts: []string{action.ID}, PrimaryEvidence: []flowir.Anchor{action.Evidence[0]}, Status: flowir.Unknown}
		doc := flowir.Document{SchemaVersion: flowir.SchemaVersion, Basis: basis, Facts: facts, Architecture: flowir.ArchitectureSlice{EntryPoints: []string{entry.FlowID}, Boundaries: []string{"ui", "application", "graph:" + backend}, Components: []string{entry.Anchor.Path, action.Subject}, Relations: []string{"call", "transition"}}, Current: flowir.Flow{ID: entry.FlowID, FlowKey: entry.FlowID, EntryPointFact: entryFact.ID, Steps: []flowir.Step{step}, Status: flowir.Unknown}, Unknowns: []flowir.UnknownDetail{{ID: flowir.Hash("unknown", "ambiguous_route_transition", action.ID), Question: "Which observed route transition is the result of this action?", Reason: "ambiguous_route_transition", RelatedSteps: []string{step.ID}, Evidence: []flowir.Anchor{action.Evidence[0]}}}}
		doc.CausalEdges = causalEdges(doc)
		attachDebtGuidance(&doc)
		return doc, flowir.Validate(doc)
	}
	completeEventChain := confirmation != nil && terminalResult != nil && eventDispatch != nil && notifierState != nil && listenerCondition != nil && listenerRoute != nil && terminalResult.Subject == confirmation.Subject && listenerRoute.Subject == listenerCondition.Subject
	if transition == nil {
		if completeEventChain {
			return assembleEventListenerFlow(basis, entry, entryFact, facts, action, confirmation, terminalResult, eventDispatch, notifierState, listenerCondition, listenerRoute, backend)
		}
		step := flowir.Step{ID: flowir.Hash(entry.FlowID, entryFact.ID, action.ID), BehaviorKey: entry.FlowID + ":entry:" + action.Subject, Order: 1, Actor: actionActor, TriggerFact: entryFact.ID, BehaviorFacts: []string{action.ID}, PrimaryEvidence: []flowir.Anchor{action.Evidence[0]}, Status: flowir.Unknown}
		doc := flowir.Document{SchemaVersion: flowir.SchemaVersion, Basis: basis, Facts: facts, Architecture: flowir.ArchitectureSlice{EntryPoints: []string{entry.FlowID}, Boundaries: []string{"ui", "application", "graph:" + backend}, Components: []string{entry.Anchor.Path, action.Subject}, Relations: []string{"call"}}, Current: flowir.Flow{ID: entry.FlowID, FlowKey: entry.FlowID, EntryPointFact: entryFact.ID, Steps: []flowir.Step{step}, Status: flowir.Unknown}, Unknowns: []flowir.UnknownDetail{{ID: flowir.Hash("unknown", "missing_route_transition", action.ID), Question: "Which visible route or UI result follows this action?", Reason: "missing_relation", RelatedSteps: []string{step.ID}, Evidence: []flowir.Anchor{action.Evidence[0]}}}}
		doc.CausalEdges = causalEdges(doc)
		attachDebtGuidance(&doc)
		return doc, flowir.Validate(doc)
	}
	visible := flowir.Fact{ID: flowir.Hash(flowir.SchemaVersion, "visible_result", transition.Subject, transition.Object, transition.Evidence[0].Fingerprint), Kind: "visible_result", Subject: transition.Subject, Object: transition.Object, Proof: transition.Proof, SymbolID: transition.SymbolID, Evidence: transition.Evidence, Status: flowir.Observed}
	facts = append(facts, visible)
	step1 := flowir.Step{ID: flowir.Hash(entry.FlowID, entryFact.ID, action.ID), BehaviorKey: entry.FlowID + ":entry:" + action.Subject, Order: 1, Actor: actionActor, TriggerFact: entryFact.ID, BehaviorFacts: []string{action.ID}, PrimaryEvidence: []flowir.Anchor{action.Evidence[0]}, Status: flowir.Observed}
	behavior := []string{transition.ID}
	anchors := []flowir.Anchor{transition.Evidence[0]}
	if call != nil {
		behavior = []string{call.ID, transition.ID}
		anchors = []flowir.Anchor{call.Evidence[0], transition.Evidence[0]}
	}
	step2 := flowir.Step{ID: flowir.Hash(entry.FlowID, action.ID, transition.ID), BehaviorKey: entry.FlowID + ":system:route_transition:" + transition.Object, Order: 2, Actor: "system", TriggerFact: action.ID, BehaviorFacts: behavior, ResultFacts: []string{visible.ID}, PrimaryEvidence: anchors, Status: flowir.Observed}
	steps := []flowir.Step{step1, step2}
	status := flowir.Observed
	unknowns := []flowir.UnknownDetail(nil)
	// A branch is emitted only when the route result is directly tied to the
	// same source body as the condition. The literal result remains observed on
	// that branch; the other conditional outcome is explicitly unknown unless
	// the adapter independently proves it. Never present a conditional route as
	// an unconditional result for the initiating user action.
	if condition != nil && dynamic != nil {
		conditionStep := flowir.Step{ID: flowir.Hash(entry.FlowID, action.ID, condition.ID), BehaviorKey: entry.FlowID + ":system:condition:" + condition.ID, Order: 2, Actor: "system", TriggerFact: action.ID, BehaviorFacts: []string{condition.ID}, PrimaryEvidence: []flowir.Anchor{condition.Evidence[0]}, Status: flowir.Mixed}
		observed := step2
		observed.Order = 3
		unknownStep := flowir.Step{ID: flowir.Hash(entry.FlowID, condition.ID, dynamic.ID), BehaviorKey: entry.FlowID + ":system:dynamic_dispatch:" + dynamic.Subject, Order: 4, Actor: "system", TriggerFact: condition.ID, BehaviorFacts: []string{dynamic.ID}, PrimaryEvidence: []flowir.Anchor{dynamic.Evidence[0]}, Status: flowir.Unknown}
		conditionStep.Branches = []flowir.Branch{{ID: flowir.BranchID(condition.ID, []string{observed.BehaviorKey, unknownStep.BehaviorKey}), ConditionFact: condition.ID, OutcomeStepIDs: []string{observed.ID, unknownStep.ID}, Evidence: []flowir.Anchor{condition.Evidence[0]}, Status: flowir.Unknown}}
		steps = []flowir.Step{step1, conditionStep, observed, unknownStep}
		status = flowir.Mixed
		unknowns = []flowir.UnknownDetail{{ID: flowir.Hash("unknown", "dynamic_dispatch", dynamic.ID), Question: "Which target does this dynamic dispatch reach?", Reason: "dynamic_dispatch", RelatedSteps: []string{unknownStep.ID}, Evidence: []flowir.Anchor{dynamic.Evidence[0]}}}
	} else if condition != nil && transition.Subject == condition.Subject && completeEventChain {
		// Both outcomes are already present in current source. The direct route
		// handles one condition result; otherwise execution continues to the
		// confirmation guard and the state-driven route.
		// Do not manufacture an unknown merely because the choice is made at
		// runtime—the set of possible outcomes is statically complete.
		conditionStep := flowir.Step{ID: flowir.Hash(entry.FlowID, action.ID, condition.ID), BehaviorKey: entry.FlowID + ":system:condition:" + condition.ID, Order: 2, Actor: "system", TriggerFact: action.ID, BehaviorFacts: []string{condition.ID}, PrimaryEvidence: []flowir.Anchor{condition.Evidence[0]}, Status: flowir.Observed}
		observed := step2
		observed.Order = 3
		confirmationStep := flowir.Step{ID: flowir.Hash(entry.FlowID, action.ID, confirmation.ID), BehaviorKey: entry.FlowID + ":system:confirmation:" + confirmation.ID, Order: 4, Actor: "system", TriggerFact: condition.ID, BehaviorFacts: []string{confirmation.ID}, PrimaryEvidence: []flowir.Anchor{confirmation.Evidence[0]}, Status: flowir.Observed}
		conditionStep.Branches = []flowir.Branch{{ID: flowir.BranchID(condition.ID, []string{observed.BehaviorKey, confirmationStep.BehaviorKey}), ConditionFact: condition.ID, OutcomeStepIDs: []string{observed.ID, confirmationStep.ID}, Evidence: []flowir.Anchor{condition.Evidence[0]}, Status: flowir.Observed}}
		steps = []flowir.Step{step1, conditionStep, observed, confirmationStep}
		status = flowir.Observed
		unknowns = nil
	} else if condition != nil && transition.Subject == condition.Subject {
		conditionStep := flowir.Step{ID: flowir.Hash(entry.FlowID, action.ID, condition.ID), BehaviorKey: entry.FlowID + ":system:condition:" + condition.ID, Order: 2, Actor: "system", TriggerFact: action.ID, BehaviorFacts: []string{condition.ID}, PrimaryEvidence: []flowir.Anchor{condition.Evidence[0]}, Status: flowir.Mixed}
		observed := step2
		observed.Order = 3
		unknownStep := flowir.Step{ID: flowir.Hash(entry.FlowID, condition.ID, "unproven_conditional_outcome"), BehaviorKey: entry.FlowID + ":system:conditional_outcome:unknown", Order: 4, Actor: "system", TriggerFact: condition.ID, PrimaryEvidence: []flowir.Anchor{condition.Evidence[0]}, Status: flowir.Unknown}
		conditionStep.Branches = []flowir.Branch{{ID: flowir.BranchID(condition.ID, []string{observed.BehaviorKey, unknownStep.BehaviorKey}), ConditionFact: condition.ID, OutcomeStepIDs: []string{observed.ID, unknownStep.ID}, Evidence: []flowir.Anchor{condition.Evidence[0]}, Status: flowir.Unknown}}
		steps = []flowir.Step{step1, conditionStep, observed, unknownStep}
		status = flowir.Mixed
		unknowns = []flowir.UnknownDetail{{ID: flowir.Hash("unknown", "conditional_route_alternative", condition.ID), Question: "What happens when this route condition does not take the observed /home outcome?", Reason: "conditional_route_alternative", RelatedSteps: []string{unknownStep.ID}, Evidence: []flowir.Anchor{condition.Evidence[0]}}}
	}
	// Keep provider evidence as its own causal step. This is deliberately a
	// source-backed transition, not a claim about business intent. An unsupported
	// Riverpod shape stays unknown at this exact timeline position.
	if state != nil || unknownState != nil {
		stateFact := state
		if stateFact == nil {
			stateFact = unknownState
		}
		behavior := []string{}
		anchors := []flowir.Anchor{}
		if dependency != nil {
			behavior = append(behavior, dependency.ID)
			anchors = append(anchors, dependency.Evidence[0])
		}
		if operation != nil {
			behavior = append(behavior, operation.ID)
			anchors = append(anchors, operation.Evidence[0])
		}
		if len(anchors) == 0 {
			anchors = append(anchors, stateFact.Evidence[0])
		}
		for i := range steps {
			if steps[i].Order >= 2 {
				steps[i].Order++
			}
		}
		stateStep := flowir.Step{ID: flowir.Hash(entry.FlowID, action.ID, stateFact.ID), BehaviorKey: entry.FlowID + ":system:state_change:" + stateFact.Object, Order: 2, Actor: "system", TriggerFact: action.ID, BehaviorFacts: behavior, ResultFacts: []string{stateFact.ID}, PrimaryEvidence: anchors, Status: stateFact.Status}
		steps = append([]flowir.Step{steps[0], stateStep}, steps[1:]...)
		if stateFact.Status == flowir.Unknown {
			status = flowir.Mixed
			unknowns = append(unknowns, flowir.UnknownDetail{ID: flowir.Hash("unknown", "riverpod", stateFact.ID), Question: "Which Riverpod state transition occurs here?", Reason: "unsupported_riverpod_pattern", RelatedSteps: []string{stateStep.ID}, Evidence: []flowir.Anchor{stateFact.Evidence[0]}})
		}
	}
	if repository != nil {
		repositoryStep := flowir.Step{ID: flowir.Hash(entry.FlowID, action.ID, repository.ID), BehaviorKey: entry.FlowID + ":system:repository:" + repository.Object, Order: 2, Actor: "system", TriggerFact: action.ID, BehaviorFacts: []string{repository.ID}, ResultFacts: []string{repository.ID}, PrimaryEvidence: []flowir.Anchor{repository.Evidence[0]}, Status: flowir.Observed}
		steps = insertTimelineStep(steps, repositoryStep)
	}
	if external != nil {
		trigger := action.ID
		if repository != nil {
			trigger = repository.ID
		}
		result := externalResult
		if result == nil {
			result = externalUnknown
		}
		if result != nil {
			order := 2
			if repository != nil {
				order = 3
			}
			boundaryStep := flowir.Step{ID: flowir.Hash(entry.FlowID, trigger, external.ID), BehaviorKey: entry.FlowID + ":system:external:" + external.Object, Order: order, Actor: "system", TriggerFact: trigger, BehaviorFacts: []string{external.ID}, ResultFacts: []string{result.ID}, PrimaryEvidence: []flowir.Anchor{external.Evidence[0], result.Evidence[0]}, Status: result.Status}
			steps = insertTimelineStep(steps, boundaryStep)
			if result.Status == flowir.Unknown {
				status = flowir.Mixed
				unknowns = append(unknowns, flowir.UnknownDetail{ID: flowir.Hash("unknown", "external_boundary", result.ID), Question: "What result does this external boundary produce?", Reason: "EXTERNAL_BOUNDARY_UNKNOWN", RelatedSteps: []string{boundaryStep.ID}, Evidence: []flowir.Anchor{result.Evidence[0]}})
			}
		}
	}
	// A resolved event chain is admitted only when every fact is independently
	// anchored and the route belongs to the same resolved listener body.
	if completeEventChain {
		authVisible := flowir.Fact{ID: flowir.Hash(flowir.SchemaVersion, "visible_result", listenerRoute.Subject, listenerRoute.Object, listenerRoute.Evidence[0].Fingerprint), Kind: "visible_result", Subject: listenerRoute.Subject, Object: listenerRoute.Object, Proof: listenerRoute.Proof, SymbolID: listenerRoute.SymbolID, Evidence: listenerRoute.Evidence, Status: flowir.Observed}
		facts = append(facts, authVisible)
		nextOrder := 1
		confirmationIndex := -1
		for i := range steps {
			if steps[i].Order >= nextOrder {
				nextOrder = steps[i].Order + 1
			}
			if len(steps[i].BehaviorFacts) == 1 && steps[i].BehaviorFacts[0] == confirmation.ID {
				confirmationIndex = i
			}
		}
		if confirmationIndex < 0 {
			steps = append(steps, flowir.Step{ID: flowir.Hash(entry.FlowID, action.ID, confirmation.ID), BehaviorKey: entry.FlowID + ":system:confirmation:" + confirmation.ID, Order: nextOrder, Actor: "system", TriggerFact: action.ID, BehaviorFacts: []string{confirmation.ID}, PrimaryEvidence: []flowir.Anchor{confirmation.Evidence[0]}, Status: flowir.Observed})
			confirmationIndex = len(steps) - 1
			nextOrder++
		}
		terminalStep := flowir.Step{ID: flowir.Hash(entry.FlowID, confirmation.ID, terminalResult.ID), BehaviorKey: entry.FlowID + ":system:terminal:" + terminalResult.Object, Order: nextOrder, Actor: "system", TriggerFact: confirmation.ID, BehaviorFacts: []string{terminalResult.ID}, ResultFacts: []string{terminalResult.ID}, PrimaryEvidence: []flowir.Anchor{terminalResult.Evidence[0]}, Status: flowir.Observed}
		eventStep := flowir.Step{ID: flowir.Hash(entry.FlowID, confirmation.ID, eventDispatch.ID), BehaviorKey: entry.FlowID + ":system:event:" + eventDispatch.Object, Order: nextOrder + 1, Actor: "system", TriggerFact: confirmation.ID, BehaviorFacts: []string{eventDispatch.ID}, PrimaryEvidence: []flowir.Anchor{eventDispatch.Evidence[0]}, Status: flowir.Observed}
		stateStep := flowir.Step{ID: flowir.Hash(entry.FlowID, eventDispatch.ID, notifierState.ID), BehaviorKey: entry.FlowID + ":system:notifier_state:" + notifierState.Object, Order: nextOrder + 2, Actor: "system", TriggerFact: eventDispatch.ID, BehaviorFacts: []string{notifierState.ID}, ResultFacts: []string{notifierState.ID}, PrimaryEvidence: []flowir.Anchor{notifierState.Evidence[0]}, Status: flowir.Observed}
		listenerStep := flowir.Step{ID: flowir.Hash(entry.FlowID, notifierState.ID, listenerCondition.ID), BehaviorKey: entry.FlowID + ":system:listener_condition:" + listenerCondition.Object, Order: nextOrder + 3, Actor: "system", TriggerFact: notifierState.ID, BehaviorFacts: []string{listenerCondition.ID}, PrimaryEvidence: []flowir.Anchor{listenerCondition.Evidence[0]}, Status: flowir.Observed}
		authStep := flowir.Step{ID: flowir.Hash(entry.FlowID, listenerCondition.ID, listenerRoute.ID), BehaviorKey: entry.FlowID + ":system:route_transition:" + listenerRoute.Object, Order: nextOrder + 4, Actor: "system", TriggerFact: listenerCondition.ID, BehaviorFacts: []string{listenerRoute.ID}, ResultFacts: []string{authVisible.ID}, PrimaryEvidence: []flowir.Anchor{listenerRoute.Evidence[0]}, Status: flowir.Observed}
		steps[confirmationIndex].Branches = []flowir.Branch{{ID: flowir.BranchID(confirmation.ID, []string{terminalStep.BehaviorKey, eventStep.BehaviorKey}), ConditionFact: confirmation.ID, OutcomeStepIDs: []string{terminalStep.ID, eventStep.ID}, Evidence: []flowir.Anchor{confirmation.Evidence[0]}, Status: flowir.Observed}}
		steps = append(steps, terminalStep, eventStep, stateStep, listenerStep, authStep)
	}
	boundaries := []string{"ui", "application"}
	if backend != "" {
		boundaries = append(boundaries, "graph:"+backend)
	}
	components := []string{entry.Anchor.Path, action.Subject, transition.Subject}
	relations := []string{"call", "transition"}
	if dependency != nil {
		boundaries = append(boundaries, "state")
		components = append(components, dependency.Object)
		relations = append(relations, "provider_dependency")
	}
	if operation != nil {
		components = append(components, operation.Object)
		relations = append(relations, "notifier_operation")
	}
	if state != nil {
		components = append(components, state.Object)
		relations = append(relations, "state_transition")
	}
	if unknownState != nil {
		relations = append(relations, "unknown_state")
	}
	if repository != nil {
		boundaries = append(boundaries, "data")
		components = append(components, repository.Object)
		relations = append(relations, "repository_access")
	}
	if external != nil {
		boundaries = append(boundaries, "external")
		components = append(components, external.Object)
		relations = append(relations, "external_call")
	}
	if externalResult != nil {
		relations = append(relations, "external_contract_result")
	}
	if externalUnknown != nil {
		relations = append(relations, "EXTERNAL_BOUNDARY_UNKNOWN")
	}
	doc := flowir.Document{SchemaVersion: flowir.SchemaVersion, Basis: basis, Facts: facts, Architecture: flowir.ArchitectureSlice{EntryPoints: []string{entry.FlowID}, Boundaries: boundaries, Components: components, Relations: relations}, Current: flowir.Flow{ID: entry.FlowID, FlowKey: entry.FlowID, EntryPointFact: entryFact.ID, Steps: steps, Status: status}, Unknowns: unknowns}
	doc.CausalEdges = causalEdges(doc)
	attachDebtGuidance(&doc)
	return doc, flowir.Validate(doc)
}

// assembleEventListenerFlow handles the common state-driven navigation shape
// where a user action dispatches an event and only a provider listener owns the
// visible route transition. Keeping this as its own assembly path prevents the
// final route from being shown before the event and state change that cause it.
func assembleEventListenerFlow(
	basis flowir.Basis,
	entry entrypoint.EntryPoint,
	entryFact flowir.Fact,
	facts []flowir.Fact,
	action, confirmation, terminalResult, eventDispatch, notifierState, listenerCondition, listenerRoute *flowir.Fact,
	backend string,
) (flowir.Document, error) {
	visible := flowir.Fact{ID: flowir.Hash(flowir.SchemaVersion, "visible_result", listenerRoute.Subject, listenerRoute.Object, listenerRoute.Evidence[0].Fingerprint), Kind: "visible_result", Subject: listenerRoute.Subject, Object: listenerRoute.Object, Proof: listenerRoute.Proof, SymbolID: listenerRoute.SymbolID, Evidence: listenerRoute.Evidence, Status: flowir.Observed}
	facts = append(facts, visible)
	userStep := flowir.Step{ID: flowir.Hash(entry.FlowID, entryFact.ID, action.ID), BehaviorKey: entry.FlowID + ":user:" + action.Subject, Order: 1, Actor: "user", TriggerFact: entryFact.ID, BehaviorFacts: []string{action.ID}, PrimaryEvidence: []flowir.Anchor{action.Evidence[0]}, Status: flowir.Observed}
	confirmationStep := flowir.Step{ID: flowir.Hash(entry.FlowID, action.ID, confirmation.ID), BehaviorKey: entry.FlowID + ":system:confirmation:" + confirmation.ID, Order: 2, Actor: "system", TriggerFact: action.ID, BehaviorFacts: []string{confirmation.ID}, PrimaryEvidence: []flowir.Anchor{confirmation.Evidence[0]}, Status: flowir.Observed}
	terminalStep := flowir.Step{ID: flowir.Hash(entry.FlowID, confirmation.ID, terminalResult.ID), BehaviorKey: entry.FlowID + ":system:terminal:" + terminalResult.Object, Order: 3, Actor: "system", TriggerFact: confirmation.ID, BehaviorFacts: []string{terminalResult.ID}, ResultFacts: []string{terminalResult.ID}, PrimaryEvidence: []flowir.Anchor{terminalResult.Evidence[0]}, Status: flowir.Observed}
	eventStep := flowir.Step{ID: flowir.Hash(entry.FlowID, confirmation.ID, eventDispatch.ID), BehaviorKey: entry.FlowID + ":system:event:" + eventDispatch.Object, Order: 4, Actor: "system", TriggerFact: confirmation.ID, BehaviorFacts: []string{eventDispatch.ID}, PrimaryEvidence: []flowir.Anchor{eventDispatch.Evidence[0]}, Status: flowir.Observed}
	stateStep := flowir.Step{ID: flowir.Hash(entry.FlowID, eventDispatch.ID, notifierState.ID), BehaviorKey: entry.FlowID + ":system:notifier_state:" + notifierState.Object, Order: 5, Actor: "system", TriggerFact: eventDispatch.ID, BehaviorFacts: []string{notifierState.ID}, ResultFacts: []string{notifierState.ID}, PrimaryEvidence: []flowir.Anchor{notifierState.Evidence[0]}, Status: flowir.Observed}
	listenerStep := flowir.Step{ID: flowir.Hash(entry.FlowID, notifierState.ID, listenerCondition.ID), BehaviorKey: entry.FlowID + ":system:listener_condition:" + listenerCondition.Object, Order: 6, Actor: "system", TriggerFact: notifierState.ID, BehaviorFacts: []string{listenerCondition.ID}, PrimaryEvidence: []flowir.Anchor{listenerCondition.Evidence[0]}, Status: flowir.Observed}
	routeStep := flowir.Step{ID: flowir.Hash(entry.FlowID, listenerCondition.ID, listenerRoute.ID), BehaviorKey: entry.FlowID + ":system:route_transition:" + listenerRoute.Object, Order: 7, Actor: "system", TriggerFact: listenerCondition.ID, BehaviorFacts: []string{listenerRoute.ID}, ResultFacts: []string{visible.ID}, PrimaryEvidence: []flowir.Anchor{listenerRoute.Evidence[0]}, Status: flowir.Observed}
	confirmationStep.Branches = []flowir.Branch{{ID: flowir.BranchID(confirmation.ID, []string{terminalStep.BehaviorKey, eventStep.BehaviorKey}), ConditionFact: confirmation.ID, OutcomeStepIDs: []string{terminalStep.ID, eventStep.ID}, Evidence: []flowir.Anchor{confirmation.Evidence[0]}, Status: flowir.Observed}}
	boundaries := []string{"ui", "application", "state"}
	if backend != "" {
		boundaries = append(boundaries, "graph:"+backend)
	}
	doc := flowir.Document{
		SchemaVersion: flowir.SchemaVersion,
		Basis:         basis,
		Facts:         facts,
		Architecture: flowir.ArchitectureSlice{
			EntryPoints: []string{entry.FlowID},
			Boundaries:  boundaries,
			Components:  []string{entry.Anchor.Path, action.Subject, notifierState.Subject, listenerCondition.Subject, listenerRoute.Object},
			Relations:   []string{"event_dispatch", "state_transition", "listener_condition", "route_transition"},
		},
		Current: flowir.Flow{ID: entry.FlowID, FlowKey: entry.FlowID, EntryPointFact: entryFact.ID, Steps: []flowir.Step{userStep, confirmationStep, terminalStep, eventStep, stateStep, listenerStep, routeStep}, Status: flowir.Observed},
	}
	doc.CausalEdges = causalEdges(doc)
	attachDebtGuidance(&doc)
	return doc, flowir.Validate(doc)
}

// causalEdges makes every timeline assertion navigable in both directions.
// The compiler only projects explicit trigger/behavior/result references; it
// never guesses a relation from proximity or matching prose.
func causalEdges(doc flowir.Document) []flowir.CausalEdge {
	seen := map[string]bool{}
	var edges []flowir.CausalEdge
	add := func(from, to, kind string, conditions []string, evidence []flowir.Anchor, status flowir.Status) {
		if from == "" || to == "" || from == to || len(evidence) == 0 {
			return
		}
		id := flowir.CausalEdgeID(from, to, kind, conditions)
		if seen[id] {
			return
		}
		seen[id] = true
		edges = append(edges, flowir.CausalEdge{ID: id, FromFact: from, ToFact: to, Kind: kind, Conditions: conditions, Evidence: evidence, Status: status})
	}
	byKind := map[string][]flowir.Fact{}
	for _, fact := range doc.Facts {
		byKind[fact.Kind] = append(byKind[fact.Kind], fact)
	}
	link := func(from flowir.Fact, targetKinds []string, subject, kind string, conditions []string) {
		for _, targetKind := range targetKinds {
			for _, to := range byKind[targetKind] {
				if to.Subject != subject {
					continue
				}
				status := flowir.Observed
				if from.Status == flowir.Unknown || to.Status == flowir.Unknown {
					status = flowir.Unknown
				}
				add(from.ID, to.ID, kind, conditions, to.Evidence, status)
			}
		}
	}
	for _, entry := range byKind["entry_point"] {
		for _, action := range byKind["user_action"] {
			add(entry.ID, action.ID, "enters", nil, action.Evidence, action.Status)
		}
	}
	for _, action := range byKind["user_action"] {
		link(action, []string{"call"}, action.Subject, "invokes", nil)
		link(action, []string{"condition", "provider_dependency", "repository_access", "external_call", "confirmation_condition", "route_transition"}, action.Subject, "causes", nil)
	}
	for _, call := range byKind["call"] {
		link(call, []string{"call", "condition", "provider_dependency", "repository_access", "external_call", "route_transition"}, call.Object, "invokes", nil)
	}
	for _, dependency := range byKind["provider_dependency"] {
		link(dependency, []string{"notifier_operation"}, dependency.Object, "resolves_to", nil)
	}
	for _, operation := range byKind["notifier_operation"] {
		link(operation, []string{"state_transition", "unknown_state"}, operation.Object, "changes_state", nil)
	}
	for _, condition := range byKind["condition"] {
		link(condition, []string{"route_transition", "dynamic_dispatch"}, condition.Subject, "guards", []string{condition.ID})
	}
	for _, external := range byKind["external_call"] {
		link(external, []string{"external_result", "external_boundary_unknown"}, external.Object, "produces", nil)
	}
	for _, confirmation := range byKind["confirmation_condition"] {
		link(confirmation, []string{"event_dispatch", "terminal_result"}, confirmation.Subject, "permits", []string{confirmation.ID})
	}
	// Branch outcome edges are derived from the explicit FlowIR references, not
	// source proximity. This keeps the causal map navigable even when a bounded
	// adapter proves a continuation across separate callback/listener bodies.
	steps := map[string]flowir.Step{}
	for _, step := range doc.Current.Steps {
		steps[step.ID] = step
	}
	for _, step := range doc.Current.Steps {
		for _, branch := range step.Branches {
			for _, outcomeID := range branch.OutcomeStepIDs {
				outcome := steps[outcomeID]
				ids := append(append([]string{}, outcome.BehaviorFacts...), outcome.ResultFacts...)
				if len(ids) == 0 {
					continue
				}
				to := ids[0]
				if fact, ok := func() (flowir.Fact, bool) {
					for _, candidate := range doc.Facts {
						if candidate.ID == to {
							return candidate, true
						}
					}
					return flowir.Fact{}, false
				}(); ok {
					add(branch.ConditionFact, to, "permits", []string{branch.ConditionFact}, fact.Evidence, fact.Status)
				}
			}
		}
	}
	// These joins are admitted only when the adapter has already produced one
	// unique, independently anchored event→state→listener chain.
	if len(byKind["event_dispatch"]) == 1 && len(byKind["notifier_state_transition"]) == 1 {
		add(byKind["event_dispatch"][0].ID, byKind["notifier_state_transition"][0].ID, "changes_state", nil, byKind["notifier_state_transition"][0].Evidence, flowir.Observed)
	}
	if len(byKind["notifier_state_transition"]) == 1 && len(byKind["listener_condition"]) == 1 {
		add(byKind["notifier_state_transition"][0].ID, byKind["listener_condition"][0].ID, "observed_by", nil, byKind["listener_condition"][0].Evidence, flowir.Observed)
	}
	for _, listener := range byKind["listener_condition"] {
		link(listener, []string{"route_transition"}, listener.Subject, "guards", nil)
	}
	for _, transition := range byKind["route_transition"] {
		for _, visible := range byKind["visible_result"] {
			if visible.Subject == transition.Subject && visible.Object == transition.Object {
				add(transition.ID, visible.ID, "produces", nil, visible.Evidence, visible.Status)
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	return edges
}

// attachDebtGuidance turns an unknown boundary into an actionable cognitive
// debt record without promoting it to observed behavior.
func attachDebtGuidance(doc *flowir.Document) {
	byStep := map[string][]string{}
	for _, edge := range doc.CausalEdges {
		for _, step := range doc.Current.Steps {
			for _, id := range append(append([]string{step.TriggerFact}, step.BehaviorFacts...), step.ResultFacts...) {
				if edge.FromFact == id || edge.ToFact == id {
					byStep[step.ID] = append(byStep[step.ID], edge.ID)
					break
				}
			}
		}
	}
	for i := range doc.Unknowns {
		u := &doc.Unknowns[i]
		u.DebtState = "open"
		for _, step := range u.RelatedSteps {
			u.RelatedEdges = append(u.RelatedEdges, byStep[step]...)
		}
		sort.Strings(u.RelatedEdges)
		u.RelatedEdges = uniqueStrings(u.RelatedEdges)
		switch u.Reason {
		case "unsupported_riverpod_pattern":
			u.ResolutionCriteria = []string{"A unique provider operation and its state assignment are source-anchored.", "The state consumer or visible result is source-anchored."}
			u.SuggestedEvidence = []string{"Dart Analyzer-resolved provider operation", "State-listener or widget test"}
		case "conditional_route_alternative", "dynamic_dispatch":
			u.ResolutionCriteria = []string{"Each condition outcome has a source-anchored target or explicit terminal result."}
			u.SuggestedEvidence = []string{"Resolved conditional target", "Focused route test"}
		case "EXTERNAL_BOUNDARY_UNKNOWN":
			u.ResolutionCriteria = []string{"A versioned external contract identifies the result and failure behavior."}
			u.SuggestedEvidence = []string{"Repository-local external contract"}
		default:
			u.ResolutionCriteria = []string{"A current source, test, or contract anchor proves the missing causal relation."}
			u.SuggestedEvidence = []string{"Current source anchor", "Focused behavior test or contract"}
		}
		u.Impact = []string{doc.Current.ID}
	}
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}
