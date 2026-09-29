package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"codeflow/internal/collector/contractharness"
	"codeflow/internal/collector/slicing"
)

func TestSliceReceiverMethodDisambiguation(t *testing.T) {
	const source = `package service

import (
	"errors"
	"strings"
)

type Repo struct{}
func (r *Repo) Save(id string) error { return nil }

type OrderService struct {
	repo *Repo
}

func (s *OrderService) ProcessOrder(orderID string) error {
	if orderID == "" {
		return errors.New("empty order")
	}
	trimmed := strings.TrimSpace(orderID)
	return s.repo.Save(trimmed)
}

type PaymentService struct{}

func (s *PaymentService) ProcessOrder(orderID string) error {
	return nil
}
`
	overlay := map[string]string{
		"service.go": source,
	}

	// 1. Slice OrderService.ProcessOrder
	result, err := slice("/does/not/exist", overlay, "cand-order0001", "service.go#OrderService.ProcessOrder")
	if err != nil {
		t.Fatalf("slice OrderService.ProcessOrder failed: %v", err)
	}

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := contractharness.Validate(contractharness.BaseURL+"sliced-payload.schema.json", raw); err != nil {
		t.Fatalf("schema validation failed: %v", err)
	}

	var payload slicing.SlicedPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatalf("execution reference validation failed: %v", err)
	}

	if len(payload.Steps) != 4 {
		t.Fatalf("expected 4 steps, got %d: %+v", len(payload.Steps), payload.Steps)
	}

	// Step 1: if orderID == ""
	s1 := payload.Steps[0]
	if s1.Kind != "guard" || s1.GuardCondition == nil || *s1.GuardCondition != `orderID == ""` {
		t.Errorf("step 1 unexpected: %+v", s1)
	}
	if s1.FlowContext == nil || s1.FlowContext.Statement.NodeKind != "control_header" {
		t.Errorf("step 1 missing valid flowContext: %+v", s1.FlowContext)
	}

	// Step 2: return errors.New("empty order")
	s2 := payload.Steps[1]
	if s2.Kind != "return" || !strings.Contains(s2.Description, "오류를 반환") {
		t.Errorf("step 2 unexpected: %+v", s2)
	}

	// Step 3: trimmed := strings.TrimSpace(orderID)
	s3 := payload.Steps[2]
	if s3.Kind != "mutation" || !strings.Contains(s3.Description, "trimmed") {
		t.Errorf("step 3 unexpected: %+v", s3)
	}

	// Step 4: return s.repo.Save(trimmed)
	s4 := payload.Steps[3]
	if s4.Kind != "return" {
		t.Errorf("step 4 unexpected: %+v", s4)
	}

	// Verify Edges
	// Truthy: 1 -> 2
	foundTruthy := false
	for _, edge := range payload.Edges {
		if edge.StepOrdinal != nil && *edge.StepOrdinal == 1 && edge.TargetStepOrdinal != nil && *edge.TargetStepOrdinal == 2 {
			if len(edge.Conditions) == 1 && edge.Conditions[0].Outcome == "truthy" {
				foundTruthy = true
			}
		}
	}
	if !foundTruthy {
		t.Errorf("missing truthy edge 1 -> 2 in edges: %+v", payload.Edges)
	}

	// Falsy: 1 -> 3
	foundFalsy := false
	for _, edge := range payload.Edges {
		if edge.StepOrdinal != nil && *edge.StepOrdinal == 1 && edge.TargetStepOrdinal != nil && *edge.TargetStepOrdinal == 3 {
			if len(edge.Conditions) == 1 && edge.Conditions[0].Outcome == "falsy" {
				foundFalsy = true
			}
		}
	}
	if !foundFalsy {
		t.Errorf("missing falsy edge 1 -> 3 in edges: %+v", payload.Edges)
	}

	// Sequential: 3 -> 4
	foundSeq := false
	for _, edge := range payload.Edges {
		if edge.StepOrdinal != nil && *edge.StepOrdinal == 3 && edge.TargetStepOrdinal != nil && *edge.TargetStepOrdinal == 4 {
			if len(edge.Conditions) == 0 {
				foundSeq = true
			}
		}
	}
	if !foundSeq {
		t.Errorf("missing sequential edge 3 -> 4 in edges: %+v", payload.Edges)
	}

	// No verified execution successor leaves a terminating return. An
	// unresolved call inside its return expression remains a boundary.
	for _, edge := range payload.Edges {
		if edge.StepOrdinal != nil && (*edge.StepOrdinal == 2 || *edge.StepOrdinal == 4) && edge.TargetStepOrdinal != nil {
			t.Errorf("illegal outgoing edge from terminating step %d: %+v", *edge.StepOrdinal, edge)
		}
	}

	// 2. Slice PaymentService.ProcessOrder (disambiguation)
	payResult, err := slice("/does/not/exist", overlay, "cand-pay", "service.go#PaymentService.ProcessOrder")
	if err != nil {
		t.Fatalf("slice PaymentService.ProcessOrder failed: %v", err)
	}
	paySteps := payResult["steps"].([]map[string]any)
	if len(paySteps) != 1 {
		t.Fatalf("expected 1 step for PaymentService.ProcessOrder, got %d", len(paySteps))
	}
	if paySteps[0]["symbolPath"] != "PaymentService.ProcessOrder" {
		t.Errorf("symbolPath mismatch: %v", paySteps[0]["symbolPath"])
	}

	// 3. Slice non-existent symbol returns entry_symbol_not_found error
	_, err = slice("/does/not/exist", overlay, "cand-miss", "service.go#MissingService.ProcessOrder")
	if err == nil || !strings.Contains(err.Error(), "entry_symbol_not_found") {
		t.Fatalf("expected entry_symbol_not_found, got: %v", err)
	}
}

func TestSliceLoopExitRequiresSourceEvidence(t *testing.T) {
	const source = `package service
func Run(done bool) int {
	for {
		if done {
			break
		}
		work()
	}
	return 1
}
func work() {}
`
	result, err := slice("/does/not/exist", map[string]string{"service.go": source}, "cand-loop", "service.go#Run")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var payload slicing.SlicedPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	var loopOrdinal, breakOrdinal, returnOrdinal int
	for _, step := range payload.Steps {
		switch step.Kind {
		case "branch":
			loopOrdinal = step.Ordinal
		case "break":
			breakOrdinal = step.Ordinal
		case "return":
			returnOrdinal = step.Ordinal
		}
	}
	if loopOrdinal == 0 || breakOrdinal == 0 || returnOrdinal == 0 {
		t.Fatalf("missing loop, break, or return: %+v", payload.Steps)
	}
	breakToReturn := false
	for _, edge := range payload.Edges {
		if edge.StepOrdinal != nil && *edge.StepOrdinal == loopOrdinal && edge.TargetStepOrdinal != nil && *edge.TargetStepOrdinal == returnOrdinal {
			t.Fatal("conditionless loop fabricated a falsy exit")
		}
		if edge.StepOrdinal != nil && *edge.StepOrdinal == breakOrdinal && edge.TargetStepOrdinal != nil && *edge.TargetStepOrdinal == returnOrdinal {
			breakToReturn = true
		}
	}
	if !breakToReturn {
		t.Fatal("confirmed break does not reach the following return")
	}
}

func TestSliceSwitchPreservesCaseSelectionAndBreak(t *testing.T) {
	const source = `package service
func Run(value int) int {
	result := 0
	switch value {
	case 1:
		result = 10
		break
	case 2, 3:
		return 20
	default:
		result = 30
	}
	return result
}

`
	result, err := slice("/does/not/exist", map[string]string{"service.go": source}, "cand-switch0001", "service.go#Run")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := contractharness.Validate(contractharness.BaseURL+"sliced-payload.schema.json", raw); err != nil {
		t.Fatal(err)
	}
	var payload slicing.SlicedPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	var firstCase, secondCase, defaultCase, breakStep, returnStep int
	for _, step := range payload.Steps {
		switch {
		case step.GuardCondition != nil && strings.Contains(*step.GuardCondition, "case 1"):
			firstCase = step.Ordinal
		case step.GuardCondition != nil && strings.Contains(*step.GuardCondition, "case 2, 3"):
			secondCase = step.Ordinal
		case strings.Contains(step.Description, "기본 분기"):
			defaultCase = step.Ordinal
		case step.Kind == "break":
			breakStep = step.Ordinal
		case step.Kind == "return" && strings.Contains(step.Description, "결과"):
			returnStep = step.Ordinal
		}
	}
	if firstCase == 0 || secondCase == 0 || defaultCase == 0 || breakStep == 0 || returnStep == 0 {
		t.Fatalf("switch source steps missing: %+v", payload.Steps)
	}
	hasEdge := func(from, to int, outcome, kind string) bool {
		for _, edge := range payload.Edges {
			if edge.StepOrdinal == nil || edge.TargetStepOrdinal == nil || *edge.StepOrdinal != from || *edge.TargetStepOrdinal != to {
				continue
			}
			if kind != "" && edge.Kind != kind {
				continue
			}
			if outcome == "" && len(edge.Conditions) == 0 || len(edge.Conditions) == 1 && edge.Conditions[0].Outcome == outcome {
				return true
			}
		}
		return false
	}
	if !hasEdge(firstCase, secondCase, "falsy", "") || !hasEdge(secondCase, defaultCase, "falsy", "") ||
		!hasEdge(breakStep, returnStep, "", "switch_exit") {
		t.Fatalf("switch selection or exit was lost: %+v", payload.Edges)
	}
	for _, edge := range payload.Edges {
		if edge.StepOrdinal != nil && *edge.StepOrdinal == breakStep && edge.TargetStepOrdinal != nil && *edge.TargetStepOrdinal == secondCase {
			t.Fatal("break incorrectly enters the next switch case")
		}
	}
}

func TestSliceSwitchWithoutDefaultKeepsNoMatchPath(t *testing.T) {
	const source = `package service
func Run(value int) int {
	switch value {
	case 1:
		return 1
	}
	return 2
}
`
	result, err := slice("/does/not/exist", map[string]string{"service.go": source}, "cand-switch0002", "service.go#Run")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var payload slicing.SlicedPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	var caseOrdinal, finalReturn int
	for _, step := range payload.Steps {
		if step.GuardCondition != nil && strings.Contains(*step.GuardCondition, "case 1") {
			caseOrdinal = step.Ordinal
		}
		if step.Kind == "return" && step.Anchor.ByteRange[0] == strings.LastIndex(source, "return 2") {
			finalReturn = step.Ordinal
		}
	}
	if caseOrdinal == 0 || finalReturn == 0 {
		t.Fatalf("case or no-match return missing: %+v", payload.Steps)
	}
	for _, edge := range payload.Edges {
		if edge.StepOrdinal != nil && *edge.StepOrdinal == caseOrdinal && edge.TargetStepOrdinal != nil && *edge.TargetStepOrdinal == finalReturn && len(edge.Conditions) == 1 && edge.Conditions[0].Outcome == "falsy" {
			return
		}
	}
	t.Fatalf("no-match continuation missing: %+v", payload.Edges)
}

func TestSliceSwitchFallthroughFailsClosed(t *testing.T) {
	const source = `package service
func Run(value int) int {
	switch value {
	case 1:
		fallthrough
	default:
		return 2
	}
}
`
	_, err := slice("/does/not/exist", map[string]string{"service.go": source}, "cand-switch0003", "service.go#Run")
	if err == nil || !strings.Contains(err.Error(), "entry_symbol_unsupported_control_flow") {
		t.Fatalf("fallthrough must remain an explicit analysis limit: %v", err)
	}
}

func TestSliceUnsupportedControlFlowFailsClosed(t *testing.T) {
	const source = `package service
func Run(value any) int {
	switch value.(type) {
	case int:
		return 1
	}
	return 2
}
`
	_, err := slice("/does/not/exist", map[string]string{"service.go": source}, "cand-switch", "service.go#Run")
	if err == nil || !strings.Contains(err.Error(), "entry_symbol_unsupported_control_flow") {
		t.Fatalf("unsupported type switch must not appear as a verified straight path: %v", err)
	}
}

func TestSliceLoopBackPreservesGuardOutcome(t *testing.T) {
	const source = `package service
func Run(done bool) int {
	for !done {
		if done {
			return 1
		}
	}
	return 2
}

`
	result, err := slice("/does/not/exist", map[string]string{"service.go": source}, "cand-loop-guard", "service.go#Run")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var payload slicing.SlicedPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	guard := 0
	for _, step := range payload.Steps {
		if step.Kind == "guard" {
			guard = step.Ordinal
		}
	}
	if guard == 0 {
		t.Fatal("inner guard missing")
	}
	for _, edge := range payload.Edges {
		if edge.StepOrdinal == nil || *edge.StepOrdinal != guard || edge.Kind != "loop_back" {
			continue
		}
		if len(edge.Conditions) != 1 || edge.Conditions[0].Outcome != "falsy" {
			t.Fatalf("loop-back must require the false guard outcome: %+v", edge)
		}
		return
	}
	t.Fatal("missing conditional loop-back")
}

func TestSliceContinueRunsForPostBeforeNextCondition(t *testing.T) {
	const source = `package service
func Run(limit int) int {
	count := 0
	for i := 0; i < limit; i++ {
		if i == 1 {
			continue
		}
		count++
	}
	return count
}
`
	result, err := slice("/does/not/exist", map[string]string{"service.go": source}, "cand-continue0001", "service.go#Run")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var payload slicing.SlicedPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	var loop, continuation, post int
	for _, step := range payload.Steps {
		switch {
		case step.Kind == "branch" && strings.Contains(step.Description, "i < limit"):
			loop = step.Ordinal
		case step.Kind == "continue":
			continuation = step.Ordinal
		case step.Kind == "mutation" && step.FlowContext != nil && step.FlowContext.Statement.NodeKind == "statement":
			span := source[step.Anchor.ByteRange[0]:step.Anchor.ByteRange[1]]
			if span == "i++" {
				post = step.Ordinal
			}
		}
	}
	if loop == 0 || continuation == 0 || post == 0 {
		t.Fatalf("missing loop, continue, or post step: %+v", payload.Steps)
	}
	var continueToPost, postToLoop bool
	for _, edge := range payload.Edges {
		if edge.StepOrdinal == nil || edge.TargetStepOrdinal == nil {
			continue
		}
		if *edge.StepOrdinal == continuation && *edge.TargetStepOrdinal == loop {
			t.Fatal("continue skipped the for post statement")
		}
		if *edge.StepOrdinal == continuation && *edge.TargetStepOrdinal == post {
			continueToPost = true
		}
		if *edge.StepOrdinal == post && *edge.TargetStepOrdinal == loop {
			postToLoop = true
		}
	}
	if !continueToPost || !postToLoop {
		t.Fatalf("continue or post relation missing: %+v", payload.Edges)
	}
}

func TestSliceEmptyMethodDoesNotClaimExecutableCode(t *testing.T) {
	const source = "package service\nfunc Run() {}\n"
	_, err := slice("/does/not/exist", map[string]string{"service.go": source}, "cand-empty", "service.go#Run")
	if err == nil || !strings.Contains(err.Error(), "entry_symbol_has_no_executable_steps") {
		t.Fatalf("empty body must not become a whole-function execution step: %v", err)
	}
}

func TestSliceReturnCallKeepsUnverifiedCalleeBoundary(t *testing.T) {
	const source = `package checkout
type Repository struct{}
func (r *Repository) Save(total int) (int, error) { return total, nil }
type Service struct { repo *Repository }
func (s *Service) Checkout(amount int) (int, error) {
	if amount <= 0 { return 0, nil }
	total := amount * 10
	return s.repo.Save(total)
}

`
	result, err := slice("/does/not/exist", map[string]string{"checkout.go": source}, "cand-checkout", "checkout.go#Service.Checkout")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var payload slicing.SlicedPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	for _, edge := range payload.Edges {
		if edge.Kind == "unknown_edge" && edge.ToSymbolPath == "checkout.go#s.repo.Save" && edge.ResolutionStatus == "unresolved_type" {
			return
		}
	}
	t.Fatalf("returning a call hid its unverified callee: %+v", payload.Edges)
}

func TestSliceDeclarationInitializerKeepsUnverifiedCallBoundary(t *testing.T) {
	const source = `package service
func Run() error {
	var result = save()
	return result
}
func save() error { return nil }
`
	result, err := slice("/does/not/exist", map[string]string{"service.go": source}, "cand-decl", "service.go#Run")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var payload slicing.SlicedPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	for _, edge := range payload.Edges {
		if edge.StepOrdinal != nil && *edge.StepOrdinal == 1 && edge.Kind == "unknown_edge" && strings.Contains(edge.ToSymbolPath, "save") {
			return
		}
	}
	t.Fatalf("declaration initializer call disappeared without a boundary: %+v", payload.Edges)
}

func TestSliceVerifiedReceiverCallReadsCalleeBeforeContinuation(t *testing.T) {
	const source = `package service

type Service struct { value int }

func (s *Service) ProcessOrder() int {
	s.prepare()
	return s.value
}

func (s *Service) prepare() {
	s.value = 1
	return
}
`
	result, err := slice("", map[string]string{"service.go": source}, "cand-service001", "service.go#Service.ProcessOrder")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := contractharness.Validate(contractharness.BaseURL+"sliced-payload.schema.json", raw); err != nil {
		t.Fatal(err)
	}
	var payload slicing.SlicedPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	if len(payload.Steps) != 4 || payload.Steps[0].Kind != "call" ||
		payload.Steps[1].SymbolPath != "Service.prepare" || payload.Steps[2].SymbolPath != "Service.prepare" ||
		payload.Steps[3].Kind != "return" {
		t.Fatalf("callee source was not placed between the call and its continuation: %+v", payload.Steps)
	}
	if payload.Steps[1].CallerStepOrdinal == nil || *payload.Steps[1].CallerStepOrdinal != 1 ||
		payload.Steps[1].Anchor.RepoRelativePath != "service.go" ||
		payload.Steps[1].InvocationID == payload.Steps[0].InvocationID {
		t.Fatalf("callee invocation lacks its source and caller identity: %+v", payload.Steps[1])
	}
	var call, resume bool
	for _, edge := range payload.Edges {
		if edge.Kind == "resolved_cross_file" && edge.StepOrdinal != nil && *edge.StepOrdinal == 1 &&
			edge.TargetStepOrdinal != nil && *edge.TargetStepOrdinal == 2 {
			call = true
		}
		if edge.Kind == "return" && edge.StepOrdinal != nil && *edge.StepOrdinal == 3 &&
			edge.TargetStepOrdinal != nil && *edge.TargetStepOrdinal == 4 {
			resume = true
		}
	}
	if !call || !resume {
		t.Fatalf("verified call or return-to-caller relation missing: %+v", payload.Edges)
	}
}

func TestSliceVerifiedCallResultPrecedesAssignmentAndReturn(t *testing.T) {
	const source = `package service
func Calculate(amount int) int { return amount * 2 }
func Assign(amount int) int {
	fee := Calculate(amount)
	return fee
}
func Return(amount int) int {
	return Calculate(amount)
}
`
	for _, test := range []struct {
		name       string
		entry      string
		wantedKind string
		stepCount  int
	}{
		{name: "assignment", entry: "service.go#Assign", wantedKind: "mutation", stepCount: 4},
		{name: "return", entry: "service.go#Return", wantedKind: "return", stepCount: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := slice("", map[string]string{"service.go": source}, "cand-direct001", test.entry)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if err := contractharness.Validate(contractharness.BaseURL+"sliced-payload.schema.json", raw); err != nil {
				t.Fatal(err)
			}
			var payload slicing.SlicedPayload
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
				t.Fatal(err)
			}
			if len(payload.Steps) != test.stepCount {
				t.Fatalf("call, callee, and continuation order was lost: %+v", payload.Steps)
			}
			if payload.Steps[0].Kind != "call" ||
				payload.Steps[1].SymbolPath != "Calculate" || payload.Steps[1].Kind != "return" ||
				payload.Steps[2].Kind != test.wantedKind {
				t.Fatalf("call, callee, and continuation order was lost: %+v", payload.Steps)
			}
			if test.wantedKind == "mutation" && payload.Steps[3].Kind != "return" {
				t.Fatalf("assignment did not precede the return: %+v", payload.Steps)
			}
			if payload.Steps[0].Anchor.ByteRange[1] <= payload.Steps[0].Anchor.ByteRange[0] ||
				!strings.Contains(source[payload.Steps[0].Anchor.ByteRange[0]:payload.Steps[0].Anchor.ByteRange[1]], "Calculate(amount)") {
				t.Fatalf("call source is not the actual expression: %+v", payload.Steps[0].Anchor)
			}
			var called, resumed bool
			for _, edge := range payload.Edges {
				if edge.StepOrdinal == nil || edge.TargetStepOrdinal == nil {
					continue
				}
				if edge.Kind == "resolved_cross_file" && *edge.StepOrdinal == 1 && *edge.TargetStepOrdinal == 2 {
					called = true
				}
				if edge.Kind == "return" && *edge.StepOrdinal == 2 && *edge.TargetStepOrdinal == 3 {
					resumed = true
				}
			}
			if !called || !resumed {
				t.Fatalf("call or return relation missing: %+v", payload.Edges)
			}
		})
	}
}

func TestSliceMultipleDirectCallsProduceStableRelations(t *testing.T) {
	const source = `package service
func First() { return }
func Second() { return }
func Run() {
	First()
	Second()
	return
}
`
	var baseline []byte
	for attempt := 0; attempt < 25; attempt++ {
		result, err := slice("", map[string]string{"service.go": source}, "cand-stable001", "service.go#Run")
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		var payload slicing.SlicedPayload
		if err := json.Unmarshal(encoded, &payload); err != nil {
			t.Fatal(err)
		}
		if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
			t.Fatal(err)
		}
		if attempt > 0 && !bytes.Equal(encoded, baseline) {
			t.Fatal("same source produced different direct-call relations")
		}
		baseline = encoded
	}
}

func TestSliceReportsLocalDeclarationsOnlyWhenRequested(t *testing.T) {
	const source = `package service
type State struct { turn int }
func (s *State) Apply(turn int) int {
	player := turn
	opponent := player + 1
	s.turn = opponent
	return s.turn
}
`
	overlay := map[string]string{"service.go": source}
	result, err := slice("/does/not/exist", overlay, "cand-local", "service.go#State.Apply", true)
	if err != nil {
		t.Fatal(err)
	}
	steps := result["steps"].([]map[string]any)
	if len(steps) != 4 || steps[0]["valueRole"] != "local_binding" || steps[1]["valueRole"] != "local_binding" || steps[2]["valueRole"] != nil {
		t.Fatalf("local declarations and field write were confused: %+v", steps)
	}
	legacy, err := slice("/does/not/exist", overlay, "cand-local", "service.go#State.Apply")
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range legacy["steps"].([]map[string]any) {
		if _, exists := step["valueRole"]; exists {
			t.Fatalf("new value role leaked to older slice caller: %+v", step)
		}
	}
}

func TestSliceDirectCallKeepsLocalBindingOnAssignment(t *testing.T) {
	const source = `package service
func calculate(value int) int { return value + 1 }
func Run(value int) int {
	result := calculate(value)
	return result
}
`
	result, err := slice("/does/not/exist", map[string]string{"service.go": source}, "cand-direct-local", "service.go#Run", true)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var payload slicing.SlicedPayload
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	var calls, bindings int
	for _, step := range payload.Steps {
		if step.Kind == "call" {
			calls++
			if step.ValueRole != "" {
				t.Fatalf("call inherited assignment role: %+v", step)
			}
		}
		if step.ValueRole == "local_binding" {
			bindings++
			if step.Kind != "mutation" {
				t.Fatalf("local binding changed kind: %+v", step)
			}
		}
	}
	if calls != 1 || bindings != 1 {
		t.Fatalf("direct call and local result were not kept distinct: %+v", payload.Steps)
	}
}
