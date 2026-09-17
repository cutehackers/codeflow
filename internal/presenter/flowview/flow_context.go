package flowview

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"codeflow/internal/collector/secret"
	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator/semantic"
)

// FlowContextPrecision represents the precision state of a step's Flow Context.
type FlowContextPrecision string

const (
	PrecisionExact       FlowContextPrecision = "exact"
	PrecisionUnavailable FlowContextPrecision = "unavailable"
	PrecisionUnknown     FlowContextPrecision = "unknown"
)

// FlowExpansionScope represents the visible scope of the code view.
type FlowExpansionScope string

const (
	ExpansionFlowContext FlowExpansionScope = "flow_context"
	ExpansionCallable    FlowExpansionScope = "callable"
	ExpansionFile        FlowExpansionScope = "file"
)

// StatementProjection represents the selected statement evidence projection.
type StatementProjection struct {
	NodeKind  string   `json:"nodeKind"`
	ByteRange [2]int   `json:"byteRange"`
	LineRange [2]int   `json:"lineRange"`
	Source    []string `json:"source"`
}

// StructuralContextProjection represents the enclosing structural context projection.
type StructuralContextProjection struct {
	Status    string   `json:"status"`             // "present" | "none"
	NodeKind  string   `json:"nodeKind,omitempty"` // "condition" | "callback" | "builder"
	ByteRange *[2]int  `json:"byteRange,omitempty"`
	LineRange *[2]int  `json:"lineRange,omitempty"`
	Source    []string `json:"source,omitempty"`
}

// CallableSignatureProjection represents the callable signature projection.
type CallableSignatureProjection struct {
	Text      string `json:"text"`
	ByteRange [2]int `json:"byteRange"`
	LineRange [2]int `json:"lineRange"`
}

// CallableProjection represents the full callable source projection.
type CallableProjection struct {
	ByteRange [2]int   `json:"byteRange"`
	LineRange [2]int   `json:"lineRange"`
	Source    []string `json:"source,omitempty"`
}

// DirectRelationProjection represents direct predecessor, successor, or call relation.
type DirectRelationProjection struct {
	HasRelation      bool   `json:"hasRelation"`
	Kind             string `json:"kind"` // "predecessor" | "successor" | "call" | "none"
	TargetStepID     string `json:"targetStepId,omitempty"`
	TargetSymbolPath string `json:"targetSymbolPath,omitempty"`
	Description      string `json:"description"`
}

// FlowContextLine represents a single line in the FlowView code panel.
type FlowContextLine struct {
	LineNumber int                   `json:"lineNumber"`
	Text       string                `json:"text"`
	IsHit      bool                  `json:"isHit"`    // statement highlight
	IsStruct   bool                  `json:"isStruct"` // containing condition, callback, or builder
	IsSig      bool                  `json:"isSig"`    // callable signature
	Selection  *FlowContextSelection `json:"selection,omitempty"`
}

// Selection marks only original statement bytes, even when a callable and
// statement share one source line.
type FlowContextSelection struct {
	Before string `json:"before"`
	Text   string `json:"text"`
	After  string `json:"after"`
}

// FlowContextProjection is the derived Flow Context projection for FlowView.
type FlowContextProjection struct {
	StepID             string                      `json:"stepId"`
	GenerationID       string                      `json:"generationId"`
	SnapshotID         string                      `json:"snapshotId"`
	Precision          FlowContextPrecision        `json:"precision"`
	ExpansionScope     FlowExpansionScope          `json:"expansionScope"`
	Authority          string                      `json:"authority"`
	EvidenceStatus     string                      `json:"evidenceStatus"`
	CanonicalPath      string                      `json:"canonicalPath"`
	Statement          *StatementProjection        `json:"statement,omitempty"`
	StructuralContext  StructuralContextProjection `json:"structuralContext"`
	CallableSignature  CallableSignatureProjection `json:"callableSignature"`
	Callable           *CallableProjection         `json:"callable,omitempty"`
	DirectRelation     DirectRelationProjection    `json:"directRelation"`
	SourceLimitation   string                      `json:"sourceLimitation,omitempty"`
	SourceRedacted     bool                        `json:"sourceRedacted"`
	DisplayedLineRange [2]int                      `json:"displayedLineRange"`
	DisplayedLines     []FlowContextLine           `json:"displayedLines"`
}

// DeriveFlowContextParams supplies the inputs needed to derive a step's Flow Context.
type DeriveFlowContextParams struct {
	Step                  semantic.SemanticStep
	SemanticMap           *semantic.SemanticMapIR
	SnapshotFiles         map[string][]byte
	AdapterHasFlowContext bool
	Expansion             FlowExpansionScope
	RedactSourceError     bool
	Metadata              *slicing.FlowContextMetadata
	SourceSnapshotID      string
}

// DeriveDirectRelation inspects canonical graph edges for direct relations.
func DeriveDirectRelation(stepID string, mapIR *semantic.SemanticMapIR) DirectRelationProjection {
	if mapIR == nil {
		return DirectRelationProjection{
			HasRelation: false,
			Kind:        "none",
			Description: "직접 연결된 흐름 관계 없음",
		}
	}
	// Check outgoing direct edges
	for _, edge := range mapIR.Edges {
		if edge.FromStepID == stepID {
			kind := edge.Kind
			if kind == "" {
				kind = "call"
			}
			desc := fmt.Sprintf("이어지는 호출: %s", edge.ToSymbolPath)
			if kind == "successor" {
				desc = fmt.Sprintf("다음 단계: %s", edge.ToStepID)
			}
			return DirectRelationProjection{
				HasRelation:      true,
				Kind:             kind,
				TargetStepID:     edge.ToStepID,
				TargetSymbolPath: edge.ToSymbolPath,
				Description:      desc,
			}
		}
	}
	// Check incoming direct edges
	for _, edge := range mapIR.Edges {
		if edge.ToStepID == stepID {
			return DirectRelationProjection{
				HasRelation:      true,
				Kind:             "predecessor",
				TargetStepID:     edge.FromStepID,
				TargetSymbolPath: edge.ToSymbolPath,
				Description:      fmt.Sprintf("선행 단계: %s", edge.FromStepID),
			}
		}
	}
	return DirectRelationProjection{
		HasRelation: false,
		Kind:        "none",
		Description: "직접 연결된 흐름 관계 없음",
	}
}

// DeriveFlowContext derives the Flow Context projection for a selected step.
func flowSourceSnapshotID(m *semantic.SemanticMapIR) string {
	if m == nil {
		return ""
	}
	if m.Basis.ComputedWorkspaceSnapshotID != "" {
		return m.Basis.ComputedWorkspaceSnapshotID
	}
	return m.ValidatedAgainstSnapshotID
}

func DeriveFlowContext(params DeriveFlowContextParams) *FlowContextProjection {
	step, m, meta := params.Step, params.SemanticMap, params.Metadata
	expansion := params.Expansion
	if expansion == "" {
		expansion = ExpansionFlowContext
	}
	p := &FlowContextProjection{
		StepID: step.StepID, SnapshotID: flowSourceSnapshotID(m),
		Precision: PrecisionUnavailable, ExpansionScope: expansion,
		Authority: "candidate", EvidenceStatus: "unverified",
		CanonicalPath:     step.Anchor.RepoRelativePath,
		StructuralContext: StructuralContextProjection{Status: "unknown"},
		DirectRelation:    DeriveDirectRelation(step.StepID, m),
		DisplayedLines:    []FlowContextLine{},
	}
	if m != nil {
		p.GenerationID = m.GenerationID
		if m.Authority != "" {
			p.Authority = m.Authority
		}
	}
	path := step.Anchor.RepoRelativePath
	if params.RedactSourceError || path == "" || filepath.IsAbs(path) ||
		filepath.ToSlash(filepath.Clean(path)) != path || strings.Contains(path, "\\") ||
		path == ".." || strings.HasPrefix(path, "../") {
		p.SourceLimitation = "보안 정책에 따라 소스 위치를 표시할 수 없습니다"
		p.CanonicalPath = ""
		p.SourceRedacted = true
		return p
	}
	if p.SnapshotID == "" || params.SourceSnapshotID != p.SnapshotID {
		p.SourceLimitation = "선택한 단계의 스냅샷 소스를 사용할 수 없습니다"
		return p
	}
	source, ok := params.SnapshotFiles[path]
	if !ok {
		p.SourceLimitation = "선택한 스냅샷에서 소스를 찾을 수 없습니다"
		return p
	}
	if !utf8.Valid(source) {
		p.SourceLimitation = "소스 인코딩을 확인할 수 없습니다"
		return p
	}
	// Redact the whole immutable document before taking any display ranges.
	// Newlines are preserved, but original bytes remain the validation authority.
	redacted := secret.RedactSource(string(source))
	p.SourceRedacted = redacted.Count > 0
	lines := strings.Split(redacted.Text, "\n")
	starts := computeLineStarts(string(source))
	fallback := func(precision FlowContextPrecision, reason string) *FlowContextProjection {
		p.Precision, p.SourceLimitation = precision, reason
		p.DisplayedLineRange = boundedFallbackLineRange(step.Anchor, starts, len(lines))
		if expansion == ExpansionFile {
			p.DisplayedLineRange = [2]int{1, len(lines)}
		}
		p.DisplayedLines = renderFallbackLines(lines, p.DisplayedLineRange)
		return p
	}
	if !params.AdapterHasFlowContext {
		return fallback(PrecisionUnavailable, "어댑터가 Flow Context 기능을 선언하지 않았습니다")
	}
	if meta == nil {
		return fallback(PrecisionUnavailable, "정확한 문장 위치를 증명하는 메타데이터가 없습니다")
	}
	if meta.SnapshotID == "" || meta.SourceHash == "" || meta.CanonicalPath == "" ||
		meta.SnapshotID != p.SnapshotID || meta.CanonicalPath != path ||
		meta.SourceHash != sha256Hex(source) {
		return fallback(PrecisionUnknown, "메타데이터의 스냅샷·경로·소스 해시를 검증할 수 없습니다")
	}
	validRange := func(r [2]int) bool {
		return r[0] >= 0 && r[0] < r[1] && r[1] <= len(source) &&
			utf8.Valid(source[:r[0]]) && utf8.Valid(source[r[0]:r[1]])
	}
	contains := func(outer, inner [2]int) bool {
		return outer[0] <= inner[0] && inner[1] <= outer[1]
	}
	stmt, call, sig := meta.Statement.ByteRange, meta.Callable.ByteRange, meta.Callable.SignatureByteRange
	if meta.Statement.NodeKind != "statement" || !validRange(stmt) ||
		step.Anchor.ByteRange != stmt || step.Anchor.FileHash != meta.SourceHash ||
		step.Anchor.SpanHash != sha256Hex(source[stmt[0]:stmt[1]]) {
		return fallback(PrecisionUnknown, "선택한 근거와 문장 범위가 일치하지 않습니다")
	}
	if !validRange(call) || !validRange(sig) || !contains(call, sig) ||
		!contains(call, stmt) || call == stmt || sig[1] > stmt[0] {
		return fallback(PrecisionUnknown, "문장과 호출 단위의 포함 관계를 검증할 수 없습니다")
	}
	if m != nil {
		for _, evidence := range m.Evidence {
			for _, ref := range step.EvidenceRefs {
				if evidence.EvidenceID == ref && evidence.SnapshotID == p.SnapshotID &&
					evidence.Anchor.RepoRelativePath == path && evidence.ByteRange == stmt &&
					evidence.Anchor.FileHash == meta.SourceHash && evidence.Anchor.SpanHash == step.Anchor.SpanHash {
					p.EvidenceStatus = evidence.ValidationStatus
				}
			}
		}
	}
	// Location precision is independent of graph-closure authority. The
	// compiler may mark Evidence unknown when its closure is open, even though
	// the original byte range and hashes were verified. Preserve that status.
	if p.EvidenceStatus != "verified" && p.EvidenceStatus != "unknown" {
		return fallback(PrecisionUnknown, "선택한 문장에 연결된 소스 근거가 없습니다")
	}
	st := meta.StructuralContext
	if st.Status != "none" && st.Status != "present" {
		return fallback(PrecisionUnknown, "구조 문맥의 존재 여부를 검증할 수 없습니다")
	}
	if st.Status == "present" {
		if st.ByteRange == nil || !validRange(*st.ByteRange) || !contains(*st.ByteRange, stmt) ||
			*st.ByteRange == stmt || !contains(call, *st.ByteRange) ||
			(st.NodeKind != "condition" && st.NodeKind != "callback" && st.NodeKind != "builder") {
			return fallback(PrecisionUnknown, "문장을 감싸는 구조의 범위를 검증할 수 없습니다")
		}
	} else if st.ByteRange != nil || st.LineRange != nil || st.NodeKind != "" {
		return fallback(PrecisionUnknown, "구조 문맥 없음과 범위 메타데이터가 충돌합니다")
	}
	lineRange := func(r [2]int) [2]int {
		return [2]int{byteOffsetToLine(starts, r[0]), byteOffsetToLine(starts, r[1]-1)}
	}
	stmtLines, callLines, sigLines := lineRange(stmt), lineRange(call), lineRange(sig)
	// Optional adapter line ranges must agree with byte-derived snapshot ranges.
	if (meta.Statement.LineRange != [2]int{} && meta.Statement.LineRange != stmtLines) ||
		(meta.Callable.LineRange != [2]int{} && meta.Callable.LineRange != callLines) ||
		(meta.Callable.SignatureLineRange != [2]int{} && meta.Callable.SignatureLineRange != sigLines) {
		return fallback(PrecisionUnknown, "바이트 범위와 줄 범위가 일치하지 않습니다")
	}
	if st.Status == "present" && st.LineRange != nil && *st.LineRange != lineRange(*st.ByteRange) {
		return fallback(PrecisionUnknown, "구조의 바이트 범위와 줄 범위가 일치하지 않습니다")
	}
	p.Precision = PrecisionExact
	p.Statement = &StatementProjection{NodeKind: "statement", ByteRange: stmt, LineRange: stmtLines, Source: sliceLines(lines, stmtLines[0], stmtLines[1])}
	p.StructuralContext = StructuralContextProjection{Status: st.Status}
	if st.Status == "present" {
		lr := lineRange(*st.ByteRange)
		br := *st.ByteRange
		p.StructuralContext = StructuralContextProjection{Status: st.Status, NodeKind: st.NodeKind, ByteRange: &br, LineRange: &lr, Source: sliceLines(lines, lr[0], lr[1])}
	}
	// Adapter prose cannot replace original source text.
	p.CallableSignature = CallableSignatureProjection{Text: strings.TrimSpace(secret.RedactSource(string(source[sig[0]:sig[1]])).Text), ByteRange: sig, LineRange: sigLines}
	span := stmtLines
	if st.Status == "present" {
		span = *p.StructuralContext.LineRange
	}
	switch expansion {
	case ExpansionCallable:
		span = callLines
		p.Callable = &CallableProjection{ByteRange: call, LineRange: callLines, Source: sliceLines(lines, callLines[0], callLines[1])}
	case ExpansionFile:
		span = [2]int{1, len(lines)}
	}
	p.DisplayedLines = renderFlowLines(lines, span, stmtLines[0], stmtLines[1], p.StructuralContext, sigLines[0], sigLines[1])
	if expansion == ExpansionFlowContext {
		// Show the signature separately. Unrelated code between it and the selected
		// structure is never implicitly expanded.
		prefix := renderFlowLines(lines, [2]int{sigLines[0], min(sigLines[1], span[0]-1)}, stmtLines[0], stmtLines[1], p.StructuralContext, sigLines[0], sigLines[1])
		p.DisplayedLines = append(prefix, p.DisplayedLines...)
	}
	if len(p.DisplayedLines) > 0 {
		p.DisplayedLineRange = [2]int{p.DisplayedLines[0].LineNumber, p.DisplayedLines[len(p.DisplayedLines)-1].LineNumber}
	}
	// Byte precision must survive rendering. Whole-line highlighting would
	// falsely select an entire one-line function/class around this statement.
	p.Statement.Source = nil
	rawLines := strings.Split(string(source), "\n")
	for ln := stmtLines[0]; ln <= stmtLines[1]; ln++ {
		if rawLines[ln-1] != lines[ln-1] {
			p.SourceLimitation = "선택 문장의 일부가 마스킹되어 해당 줄의 강조를 생략했습니다"
			continue
		}
		start := max(0, stmt[0]-starts[ln-1])
		end := min(len(lines[ln-1]), stmt[1]-starts[ln-1])
		p.Statement.Source = append(p.Statement.Source, lines[ln-1][start:end])
	}
	for i := range p.DisplayedLines {
		line := &p.DisplayedLines[i]
		if !line.IsHit {
			continue
		}
		if rawLines[line.LineNumber-1] != line.Text {
			line.IsHit = false
			continue
		}
		start := max(0, stmt[0]-starts[line.LineNumber-1])
		end := min(len(line.Text), stmt[1]-starts[line.LineNumber-1])
		line.Selection = &FlowContextSelection{Before: line.Text[:start], Text: line.Text[start:end], After: line.Text[end:]}
	}
	return p
}

func computeLineStarts(text string) []int {
	starts := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func byteOffsetToLine(lineStarts []int, offset int) int {
	if offset <= 0 {
		return 1
	}
	lo := 0
	hi := len(lineStarts) - 1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if lineStarts[mid] <= offset {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo + 1
}

func sliceLines(lines []string, startLine, endLine int) []string {
	if startLine < 1 {
		startLine = 1
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	if startLine > endLine {
		return nil
	}
	return lines[startLine-1 : endLine]
}

func boundedFallbackLineRange(anchor slicing.Anchor, lineStarts []int, totalLines int) [2]int {
	if totalLines <= 0 {
		return [2]int{1, 1}
	}
	start := 1
	if anchor.ByteRange[0] > 0 {
		start = byteOffsetToLine(lineStarts, anchor.ByteRange[0])
	}
	end := start + 30
	if anchor.ByteRange[1] > anchor.ByteRange[0] {
		lineEnd := byteOffsetToLine(lineStarts, anchor.ByteRange[1])
		if lineEnd-start > 40 {
			end = start + 40
		} else if lineEnd >= start {
			end = lineEnd
		}
	}
	if end > totalLines {
		end = totalLines
	}
	if start > end {
		start = end
	}
	return [2]int{start, end}
}

func renderFallbackLines(lines []string, span [2]int) []FlowContextLine {
	var out []FlowContextLine
	start := span[0]
	end := span[1]
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	for ln := start; ln <= end; ln++ {
		out = append(out, FlowContextLine{
			LineNumber: ln,
			Text:       secret.Redact(lines[ln-1]).Text,
			IsHit:      false, // NEVER hit in fallback
			IsStruct:   false,
			IsSig:      false,
		})
	}
	return out
}

func renderFlowLines(lines []string, span [2]int, stmtStartLine, stmtEndLine int, st StructuralContextProjection, sigStartLine, sigEndLine int) []FlowContextLine {
	var out []FlowContextLine
	start := span[0]
	end := span[1]
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}

	stStartLine := 0
	stEndLine := 0
	if st.Status == "present" && st.LineRange != nil {
		stStartLine = st.LineRange[0]
		stEndLine = st.LineRange[1]
	}

	for ln := start; ln <= end; ln++ {
		isHit := ln >= stmtStartLine && ln <= stmtEndLine
		isStruct := !isHit && stStartLine > 0 && ln >= stStartLine && ln <= stEndLine
		isSig := !isHit && !isStruct && ln >= sigStartLine && ln <= sigEndLine
		out = append(out, FlowContextLine{
			LineNumber: ln,
			Text:       secret.Redact(lines[ln-1]).Text,
			IsHit:      isHit,
			IsStruct:   isStruct,
			IsSig:      isSig,
		})
	}
	return out
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
