package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"codeflow/internal/presenter/flowview"
)

func requestedFlowOverviewInputSchema(target any) map[string]any {
	text := map[string]any{"type": "string", "minLength": 1}
	stepIDs := map[string]any{"type": "array", "minItems": 1, "items": text}
	block := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"stepIds"}, "properties": map[string]any{"stepIds": stepIDs}}
	segment := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"segmentId", "purpose", "inclusionReason", "stepIds", "blocks"}, "properties": map[string]any{
		"segmentId": text, "purpose": text, "inclusionReason": text, "stepIds": stepIDs, "blocks": map[string]any{"type": "array", "minItems": 1, "items": block},
	}}
	relation := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"fromSegmentId", "toSegmentId", "fromStepId", "toStepId", "edgeIndexes"}, "properties": map[string]any{
		"fromSegmentId": text, "toSegmentId": text, "fromStepId": text, "toStepId": text, "edgeIndexes": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "integer", "minimum": 0}},
	}}
	category := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"categoryId", "title", "segmentIds"}, "properties": map[string]any{"categoryId": text, "title": text, "segmentIds": map[string]any{"type": "array", "minItems": 1, "uniqueItems": true, "items": text}}}
	return map[string]any{"type": "object", "required": []string{"viewId", "segments", "relations"}, "properties": map[string]any{"target": target, "viewId": text, "categories": map[string]any{"type": "array", "minItems": 1, "items": category}, "segments": map[string]any{"type": "array", "minItems": 1, "items": segment}, "relations": map[string]any{"type": "array", "items": relation}}}
}

func (s *Server) processRequestedFlowOverview(ctx context.Context, args map[string]any) (any, error) {
	viewID, _ := args["viewId"].(string)
	if strings.TrimSpace(viewID) == "" {
		return nil, fmt.Errorf("viewId is required")
	}
	target := s.resolveTarget(args["target"])
	baseURL, authToken, err := s.requestedFlowViewEndpoint(ctx, target)
	if err != nil {
		return nil, err
	}
	proposal := map[string]any{"viewId": viewID, "segments": args["segments"], "relations": args["relations"]}
	if categories, supplied := args["categories"]; supplied {
		proposal["categories"] = categories
	}
	body, err := json.Marshal(proposal)
	if err != nil {
		return nil, fmt.Errorf("encode overview proposal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/view/overview", strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("create overview request: %w", err)
	}
	req.Header.Set("X-CodeFlow-Token", authToken)
	req.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("create overview: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&failure)
		return nil, fmt.Errorf("create overview: HTTP %d: %s", response.StatusCode, failure.Message)
	}
	// A successful View contains retained source and can exceed compact payload
	// limits. Decode only the fields this operation returns, without truncating it.
	var view struct {
		ViewID                string `json:"viewId"`
		RequestedFlowOverview any    `json:"requestedFlowOverview"`
	}
	if err := json.NewDecoder(response.Body).Decode(&view); err != nil {
		return nil, fmt.Errorf("decode overview: %w", err)
	}
	if view.ViewID == "" || view.RequestedFlowOverview == nil {
		return nil, fmt.Errorf("persisted overview is incomplete")
	}
	return map[string]any{"status": "ready", "viewId": view.ViewID, "url": baseURL + "/?token=" + url.QueryEscape(authToken) + "&viewId=" + url.QueryEscape(view.ViewID), "requestedFlowOverview": view.RequestedFlowOverview}, nil
}

// Reuse the active browser coordinator for immutable view operations. Creating
// another snapshot engine merely to save/open a view changes the live head.
func (s *Server) requestedFlowViewEndpoint(ctx context.Context, target string) (string, string, error) {
	canonicalTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", "", fmt.Errorf("resolve overview target: %w", err)
	}
	base, token, err := requestedFlowViewCoordinatorEndpoint(ctx, canonicalTarget)
	if err != nil || base != "" {
		return base, token, err
	}
	unlock, err := flowview.LockLiveCoordinatorStartup(ctx, canonicalTarget)
	if err != nil {
		return "", "", err
	}
	defer unlock()
	// Another process may have completed startup while this caller was waiting.
	base, token, err = requestedFlowViewCoordinatorEndpoint(ctx, canonicalTarget)
	if err != nil || base != "" {
		return base, token, err
	}
	fv, err := s.getLiveCoordinator(canonicalTarget)
	if err != nil {
		return "", "", err
	}
	return "http://" + fv.Addr(), fv.AuthToken(), nil
}

func requestedFlowViewCoordinatorEndpoint(ctx context.Context, canonicalTarget string) (string, string, error) {
	record, err := flowview.ReadLiveCoordinator(canonicalTarget)
	if err != nil {
		return "", "", err
	}
	if record != nil {
		parsed, parseErr := url.Parse(record.URL)
		host := ""
		if parsed != nil {
			host = parsed.Hostname()
		}
		ip := net.ParseIP(host)
		recordRoot, rootErr := filepath.EvalSymlinks(record.RepoRoot)
		if parseErr != nil || parsed.Scheme != "http" || parsed.User != nil || (host != "localhost" && (ip == nil || !ip.IsLoopback())) || rootErr != nil || recordRoot != canonicalTarget {
			return "", "", fmt.Errorf("invalid overview coordinator record")
		}
		base := "http://" + parsed.Host
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/views", nil)
		if err != nil {
			return "", "", err
		}
		request.Header.Set("X-CodeFlow-Token", record.Token)
		client := &http.Client{Timeout: time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK || response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
				return base, record.Token, nil
			}
			return "", "", fmt.Errorf("overview coordinator: HTTP %d", response.StatusCode)
		}
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		if err := flowview.RemoveLiveCoordinatorIfOwned(canonicalTarget, record.URL, record.PID); err != nil {
			return "", "", err
		}
	}
	return "", "", nil
}

func (s *Server) openRequestedFlowView(ctx context.Context, target, viewID string) (any, error) {
	base, token, err := s.requestedFlowViewEndpoint(ctx, target)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/view?viewId="+url.QueryEscape(viewID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-CodeFlow-Token", token)
	response, err := (&http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("open saved FlowView: HTTP %d", response.StatusCode)
	}
	var view struct {
		ViewID string `json:"viewId"`
	}
	if err := json.NewDecoder(response.Body).Decode(&view); err != nil {
		return nil, err
	}
	if view.ViewID != viewID {
		return nil, fmt.Errorf("open saved FlowView: result identity differs")
	}
	viewURL := base + "/?token=" + url.QueryEscape(token) + "&viewId=" + url.QueryEscape(viewID)
	return map[string]any{"status": "ready", "viewId": viewID, "url": viewURL, "viewUrl": viewURL, "token": token}, nil
}
