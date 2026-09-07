package flowview

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"codeflow/internal/semantic"
)

// handleSemanticApprovalHistory implements GET
// /api/semantic/approval-history. The configured workspace is the only
// repository authority. Request values select one exact proposal/evidence-pack
// pair and cannot supply workspace, actor, session, snapshot, or map identity.
func (s *Server) handleSemanticApprovalHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		semanticJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	query, err := parseApprovalHistoryHTTPQuery(r.URL.RawQuery)
	if err != nil {
		semanticJSONError(w, http.StatusBadRequest, "approval_invalid", "approval history request is invalid")
		return
	}
	target, err := filepath.Abs(s.repoRoot)
	if err != nil {
		writeSemanticApprovalHistoryError(w, &semantic.ApprovalUnauthorizedError{Reason: "approval workspace is unavailable"})
		return
	}
	access, err := s.authorizeSemanticApproval(r.Context(), target)
	if err != nil {
		writeSemanticApprovalHistoryError(w, err)
		return
	}
	result, err := s.QuerySemanticApprovalHistory(r.Context(), access, query)
	if err != nil {
		writeSemanticApprovalHistoryError(w, err)
		return
	}
	data, err := json.Marshal(result)
	if err != nil {
		semanticJSONError(w, http.StatusBadRequest, "approval_invalid", "approval history request is invalid")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

// QuerySemanticApprovalHistory reads one exact durable history through the
// server's shared semantic approval service.
func (s *Server) QuerySemanticApprovalHistory(ctx context.Context, access semantic.ApprovalAccess, query semantic.ApprovalHistoryQuery) (semantic.ApprovalHistoryResult, error) {
	var zero semantic.ApprovalHistoryResult
	if s == nil || s.approvalService == nil {
		return zero, semantic.ErrApprovalExecutionUnavailable
	}
	return s.approvalService.QueryApprovalHistory(ctx, access, query)
}

func parseApprovalHistoryHTTPQuery(raw string) (semantic.ApprovalHistoryQuery, error) {
	var zero semantic.ApprovalHistoryQuery
	if raw == "" {
		return zero, errors.New("approval history query is missing")
	}
	for _, field := range strings.Split(raw, "&") {
		if field == "" {
			return zero, errors.New("approval history query contains an empty field")
		}
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return zero, errors.New("approval history query encoding is invalid")
	}
	for key, items := range values {
		switch key {
		case "proposalId", "evidencePackId":
			if len(items) != 1 || !validApprovalHistoryHTTPID(items[0]) {
				return zero, errors.New("approval history identity is invalid")
			}
		case "token":
			// Token is normally carried in the header. It remains an allowed
			// transport parameter for compatibility with the existing middleware,
			// but duplicate token values are never accepted.
			if len(items) != 1 || strings.TrimSpace(items[0]) == "" {
				return zero, errors.New("approval history token is duplicated")
			}
		default:
			return zero, errors.New("approval history query contains an unknown key")
		}
	}
	proposalIDs, proposalOK := values["proposalId"]
	packIDs, packOK := values["evidencePackId"]
	if !proposalOK || !packOK || len(proposalIDs) != 1 || len(packIDs) != 1 || !validApprovalHistoryHTTPID(proposalIDs[0]) || !validApprovalHistoryHTTPID(packIDs[0]) {
		return zero, errors.New("approval history requires one proposal and evidence-pack identity")
	}
	return semantic.ApprovalHistoryQuery{ProposalID: proposalIDs[0], EvidencePackID: packIDs[0]}, nil
}

func requestAuthToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	if token := r.Header.Get("X-CodeFlow-Token"); token != "" {
		return token
	}
	if r.URL == nil {
		return ""
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return ""
	}
	tokens, ok := values["token"]
	if !ok || len(tokens) != 1 || strings.TrimSpace(tokens[0]) == "" {
		return ""
	}
	return tokens[0]
}

func validApprovalHistoryHTTPID(value string) bool {
	return semantic.ValidApprovalLifecycleID(value)
}

func writeSemanticApprovalHistoryError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		semanticJSONError(w, http.StatusRequestTimeout, "approval_unavailable", "approval history could not be read")
		return
	}
	var unauthenticated *semantic.ApprovalUnauthenticatedError
	var unauthorized *semantic.ApprovalUnauthorizedError
	if errors.As(err, &unauthenticated) || errors.As(err, &unauthorized) {
		writeSemanticApprovalAccessError(w, err)
		return
	}
	status := http.StatusBadRequest
	code := "approval_invalid"
	switch {
	case errors.Is(err, semantic.ErrApprovalHistoryUnavailable), errors.Is(err, semantic.ErrApprovalExecutionUnavailable):
		status = http.StatusNotFound
		code = "approval_unavailable"
	}
	semanticJSONError(w, status, code, "approval history could not be read")
}

func isAllowedLoopbackHost(raw string) bool {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, " \t\r\n") {
		return false
	}
	host := raw
	bracketed := false
	if strings.HasPrefix(raw, "[") {
		bracketed = true
		close := strings.IndexByte(raw, ']')
		if close < 0 {
			return false
		}
		host = raw[1:close]
		if strings.ContainsAny(host, "[]") {
			return false
		}
		if close+1 < len(raw) {
			if raw[close+1] != ':' || !validLoopbackPort(raw[close+2:]) {
				return false
			}
		}
	} else if strings.ContainsAny(raw, "[]") {
		return false
	} else if strings.Count(raw, ":") == 1 {
		parsedHost, port, err := net.SplitHostPort(raw)
		if err != nil || !validLoopbackPort(port) {
			return false
		}
		host = parsedHost
	} else if strings.Contains(raw, ":") {
		// Bare IPv6 authorities are accepted only for the canonical loopback
		// address. A bracketed address is required when a port is present.
		host = raw
	}
	host = strings.ToLower(host)
	if bracketed && host != "::1" {
		return false
	}
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func validLoopbackPort(port string) bool {
	if port == "" {
		return false
	}
	for _, digit := range port {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	value, err := strconv.Atoi(port)
	return err == nil && value >= 0 && value <= 65535
}

func isAllowedLoopbackURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || !strings.EqualFold(parsed.Scheme, "http") || parsed.Host == "" || parsed.User != nil {
		return false
	}
	return isAllowedLoopbackHost(parsed.Host)
}
