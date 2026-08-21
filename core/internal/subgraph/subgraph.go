// Package subgraph implements the Domain Subgraph Extractor.
// It synthesizes multi-step, evidence-backed business journeys across
// UI triggers, service logic, state mutations, and external I/O boundaries.
package subgraph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"codeflow/core/internal/codegraph"
	"codeflow/core/internal/flowir"
	"codeflow/core/internal/manifest"
)

type Phase string

const (
	PhaseTrigger       Phase = "trigger"        // App init, user tap, notification receive, route enter
	PhaseExecution     Phase = "execution"      // Token retrieval, business computation, service dispatch
	PhaseStateMutation Phase = "state_mutation" // Local cache, secure storage, Riverpod/Bloc state update
	PhaseIO            Phase = "io"             // Remote HTTP API call, push token registration endpoint
	PhaseReaction      Phase = "reaction"       // Navigation, toast/modal feedback, event broadcast
	PhaseTeardown      Phase = "teardown"       // Token invalidation, logout cleanup, cancel listener
)

type Role string

const (
	RoleUI         Role = "ui"
	RoleService    Role = "service"
	RoleRepository Role = "repository"
	RoleState      Role = "state"
	RoleAPI        Role = "api"
	RoleHandler    Role = "handler"
)

type Node struct {
	ID          string        `json:"id"`
	Symbol      string        `json:"symbol"`
	Anchor      flowir.Anchor `json:"anchor"`
	Phase       Phase         `json:"phase"`
	Role        Role          `json:"role"`
	Summary     string        `json:"summary"`
	CodeSnippet string        `json:"code_snippet,omitempty"`
}

type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"` // "call", "event", "state_change", "io_sync", "stream"
	Desc string `json:"description,omitempty"`
}

type Step struct {
	Index       int      `json:"index"`
	Phase       Phase    `json:"phase"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	NodeIDs     []string `json:"node_ids"`
	Delta       string   `json:"delta,omitempty"`
}

type DomainJourney struct {
	Topic       string `json:"topic"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Steps       []Step `json:"steps"`
	TotalNodes  int    `json:"total_nodes"`
	TotalEdges  int    `json:"total_edges"`
}

type DomainSubgraph struct {
	Topic   string         `json:"topic"`
	Backend string         `json:"backend"`
	Nodes   []Node         `json:"nodes"`
	Edges   []Edge         `json:"edges"`
	Journey *DomainJourney `json:"journey,omitempty"`
}

// TokenizeQuery extracts keywords from Korean and English domain queries.
func TokenizeQuery(query string) []string {
	tokens := []string{}
	// Normalization & synonym expansion map
	synonyms := map[string][]string{
		"푸시":    {"push", "fcm", "notification", "token"},
		"푸시토큰":  {"push", "token", "fcm", "device"},
		"토큰":    {"token", "auth", "session"},
		"등록":    {"register", "save", "post", "send", "update"},
		"발급":    {"get", "request", "issue", "fetch"},
		"로그인":   {"login", "signin", "auth", "session"},
		"인증":    {"auth", "authentication", "session", "token"},
		"결제":    {"payment", "pay", "order", "charge", "checkout"},
		"주문":    {"order", "cart", "checkout", "payment"},
		"장바구니":  {"cart", "item", "order"},
		"권한":    {"permission", "request", "grant"},
		"블루투스":  {"ble", "bluetooth", "device", "scan", "connect"},
		"채팅":    {"chat", "message", "websocket", "channel"},
		"알림":    {"notification", "push", "message", "alert"},
	}

	clean := strings.ToLower(query)
	for korean, terms := range synonyms {
		if strings.Contains(clean, korean) {
			tokens = append(tokens, terms...)
		}
	}

	words := regexp.MustCompile(`[A-Za-z0-9_]+`).FindAllString(clean, -1)
	for _, w := range words {
		if len(w) >= 2 && w != "the" && w != "and" && w != "for" && w != "with" {
			tokens = append(tokens, w)
		}
	}

	// deduplicate
	seen := map[string]bool{}
	unique := []string{}
	for _, t := range tokens {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" && !seen[t] {
			seen[t] = true
			unique = append(unique, t)
		}
	}
	if len(unique) == 0 && strings.TrimSpace(query) != "" {
		unique = append(unique, strings.ToLower(strings.TrimSpace(query)))
	}
	return unique
}

// Extract discovers, traverses, and synthesizes a full domain subgraph for the query.
func Extract(ctx context.Context, repo string, query string, depth int, basis *flowir.Basis, graph *codegraph.Client) (*DomainSubgraph, error) {
	if depth <= 0 {
		depth = 2
	}
	if depth > 4 {
		depth = 4
	}
	tokens := TokenizeQuery(query)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("no search seeds could be derived from query %q", query)
	}

	if basis == nil {
		b, err := manifest.Capture(repo)
		if err != nil {
			return nil, fmt.Errorf("capture basis: %w", err)
		}
		basis = &b
	}

	if graph == nil {
		graph = codegraph.New("")
	}

	rels, err := graph.DomainSubgraph(ctx, repo, tokens, depth)
	if err != nil {
		return nil, err
	}

	nodeMap := map[string]*Node{}
	edges := []Edge{}
	seenEdges := map[string]bool{}

	// Convert codegraph anchors to domain nodes
	for _, rel := range rels {
		for _, a := range []codegraph.Anchor{rel.From, rel.To} {
			if a.Path == "" || a.Symbol == "" {
				continue
			}
			id := fmt.Sprintf("%s:%s", a.Path, a.Symbol)
			if _, exists := nodeMap[id]; !exists {
				phase, role, summary := classifySymbol(a.Path, a.Symbol)
				codeSnippet := readCodeSnippet(repo, a.Path, a.ByteStart, a.ByteEnd)
				nodeMap[id] = &Node{
					ID:          id,
					Symbol:      a.Symbol,
					Anchor:      flowir.Anchor{Path: a.Path, Symbol: a.Symbol, FileHash: a.FileHash, Revision: a.Revision},
					Phase:       phase,
					Role:        role,
					Summary:     summary,
					CodeSnippet: codeSnippet,
				}
			}
		}
		fromID := fmt.Sprintf("%s:%s", rel.From.Path, rel.From.Symbol)
		toID := fmt.Sprintf("%s:%s", rel.To.Path, rel.To.Symbol)
		edgeKey := fmt.Sprintf("%s->%s:%s", fromID, toID, rel.Kind)
		if !seenEdges[edgeKey] && fromID != toID {
			seenEdges[edgeKey] = true
			edges = append(edges, Edge{
				From: fromID,
				To:   toID,
				Kind: rel.Kind,
				Desc: describeEdge(rel.Kind, fromID, toID),
			})
		}
	}

	nodes := make([]Node, 0, len(nodeMap))
	for _, n := range nodeMap {
		nodes = append(nodes, *n)
	}

	// Sort nodes by lifecycle phase and ID
	phaseOrder := map[Phase]int{
		PhaseTrigger:       1,
		PhaseExecution:     2,
		PhaseStateMutation: 3,
		PhaseIO:            4,
		PhaseReaction:      5,
		PhaseTeardown:      6,
	}
	sort.Slice(nodes, func(i, j int) bool {
		pi := phaseOrder[nodes[i].Phase]
		pj := phaseOrder[nodes[j].Phase]
		if pi != pj {
			return pi < pj
		}
		return nodes[i].ID < nodes[j].ID
	})

	journey := synthesizeJourney(query, nodes, edges)

	return &DomainSubgraph{
		Topic:   query,
		Backend: graph.Backend,
		Nodes:   nodes,
		Edges:   edges,
		Journey: journey,
	}, nil
}

func classifySymbol(path, symbol string) (Phase, Role, string) {
	lowerP := strings.ToLower(path)
	lowerS := strings.ToLower(symbol)

	if strings.Contains(lowerP, "ui") || strings.Contains(lowerP, "page") || strings.Contains(lowerP, "view") || strings.Contains(lowerP, "widget") || strings.Contains(lowerS, "tap") || strings.Contains(lowerS, "click") || strings.Contains(lowerS, "initstate") || strings.Contains(lowerS, "lifecycle") {
		return PhaseTrigger, RoleUI, fmt.Sprintf("UI 또는 생명주기 이벤트 진입: %s", symbol)
	}
	if strings.Contains(lowerP, "api") || strings.Contains(lowerP, "remote") || strings.Contains(lowerP, "network") || strings.Contains(lowerS, "post") || strings.Contains(lowerS, "http") || strings.Contains(lowerS, "request") {
		return PhaseIO, RoleAPI, fmt.Sprintf("백엔드 원격 API 동기화 및 전송: %s", symbol)
	}
	if strings.Contains(lowerP, "storage") || strings.Contains(lowerP, "cache") || strings.Contains(lowerP, "db") || strings.Contains(lowerP, "bloc") || strings.Contains(lowerP, "notifier") || strings.Contains(lowerS, "emit") || strings.Contains(lowerS, "save") || strings.Contains(lowerS, "set") {
		return PhaseStateMutation, RoleState, fmt.Sprintf("로컬 저장소 캐시 또는 앱 상태 갱신: %s", symbol)
	}
	if strings.Contains(lowerP, "repo") || strings.Contains(lowerS, "repository") {
		return PhaseStateMutation, RoleRepository, fmt.Sprintf("도메인 데이터 리포지토리 접근: %s", symbol)
	}
	if strings.Contains(lowerS, "listen") || strings.Contains(lowerS, "onmessage") || strings.Contains(lowerS, "stream") || strings.Contains(lowerS, "subscribe") {
		return PhaseReaction, RoleHandler, fmt.Sprintf("비동기 스트림 / 알림 이벤트 핸들러: %s", symbol)
	}
	if strings.Contains(lowerS, "get") || strings.Contains(lowerS, "fetch") || strings.Contains(lowerS, "register") || strings.Contains(lowerS, "service") {
		return PhaseExecution, RoleService, fmt.Sprintf("도메인 서비스 로직 실행: %s", symbol)
	}
	return PhaseExecution, RoleService, fmt.Sprintf("비즈니스 인과 단위: %s", symbol)
}

func describeEdge(kind, from, to string) string {
	switch kind {
	case "call":
		return fmt.Sprintf("%s -> %s 호출", from, to)
	case "stream_listen":
		return fmt.Sprintf("%s -> %s 이벤트 스트림 구독", from, to)
	case "state_change":
		return fmt.Sprintf("%s -> %s 상태 전이", from, to)
	case "io_sync":
		return fmt.Sprintf("%s -> %s 네트워크/DB I/O", from, to)
	default:
		return fmt.Sprintf("%s -> %s 인과 연결", from, to)
	}
}

func readCodeSnippet(repo, path string, start, end int) string {
	full := filepath.Join(repo, filepath.FromSlash(path))
	b, err := os.ReadFile(full)
	if err != nil || len(b) == 0 {
		return ""
	}
	if start < 0 {
		start = 0
	}
	if end > len(b) || end <= start {
		end = start + 500
	}
	if end > len(b) {
		end = len(b)
	}
	return string(b[start:end])
}

func synthesizeJourney(topic string, nodes []Node, edges []Edge) *DomainJourney {
	phaseGroups := map[Phase][]Node{}
	for _, n := range nodes {
		phaseGroups[n.Phase] = append(phaseGroups[n.Phase], n)
	}

	steps := []Step{}
	stepIndex := 1

	addStep := func(phase Phase, title, desc string) {
		group := phaseGroups[phase]
		if len(group) == 0 {
			return
		}
		nodeIDs := make([]string, 0, len(group))
		summaries := make([]string, 0, len(group))
		for _, n := range group {
			nodeIDs = append(nodeIDs, n.ID)
			summaries = append(summaries, n.Summary)
		}
		steps = append(steps, Step{
			Index:       stepIndex,
			Phase:       phase,
			Title:       title,
			Description: desc + " (" + strings.Join(summaries, ", ") + ")",
			NodeIDs:     nodeIDs,
			Delta:       fmt.Sprintf("%d개 요소 관찰됨", len(nodeIDs)),
		})
		stepIndex++
	}

	addStep(PhaseTrigger, "1단계: 트리거 및 진입 (Trigger & Entry)", "사용자 인터랙션 또는 시스템 생명주기 시작")
	addStep(PhaseExecution, "2단계: 도메인 로직 실행 (Execution & Processing)", "데이터 획득 및 핵심 비즈니스 로직 연산")
	addStep(PhaseStateMutation, "3단계: 상태 변경 및 로컬 저장 (State & Local Cache)", "상태 전이(State Delta) 및 로컬 DB/캐시 반영")
	addStep(PhaseIO, "4단계: 원격 동기화 및 백엔드 I/O (Remote Sync & API)", "서버 API 통신 및 원격 엔드포인트 연동")
	addStep(PhaseReaction, "5단계: 반응 및 이벤트 핸들링 (Reaction & Stream)", "화면 피드백, 스트림 수신 및 후속 작업 처리")

	return &DomainJourney{
		Topic:       topic,
		Title:       fmt.Sprintf("%s 관련 완전한 비즈니스 플로우 여정", topic),
		Description: fmt.Sprintf("%d개의 코드 심볼과 %d개의 인과 연결망으로 복원된 엔드투엔드 비즈니스 흐름입니다.", len(nodes), len(edges)),
		Steps:       steps,
		TotalNodes:  len(nodes),
		TotalEdges:  len(edges),
	}
}
