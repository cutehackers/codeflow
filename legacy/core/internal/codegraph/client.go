// Package codegraph is the narrow, versioned HTTP boundary to CodeGraph.
// It does not turn graph output into facts: callers must validate every
// relationship against the current worktree before it can enter FlowIR.
package codegraph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const ContractVersion = "1"

type Failure struct{ Code, Message string }

func (e *Failure) Error() string { return e.Code + ": " + e.Message }

type Anchor struct {
	Path      string `json:"path"`
	Symbol    string `json:"symbol"`
	ByteStart int    `json:"byte_start"`
	ByteEnd   int    `json:"byte_end"`
	FileHash  string `json:"file_hash"`
	Revision  string `json:"revision"`
}
type Relationship struct {
	From Anchor `json:"from"`
	To   Anchor `json:"to"`
	Kind string `json:"kind"`
}

type Client struct {
	BaseURL string
	HTTP    *http.Client
	Backend string
}

func New(baseURL string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// Relationships discovers the minimal slice using the public REST tool bridge.
// A compatible service can return either {relationships:[...]} or wrap that
// payload once in result/data; both are the documented bridge result shapes.
func (c *Client) Relationships(ctx context.Context, repository, flowID string) ([]Relationship, error) {
	if c == nil || c.BaseURL == "" {
		rels, err := DartStructuralRelationships(repository, flowID)
		if err == nil {
			c.Backend = "owned_dart_structural"
		}
		return rels, err
	}
	if err := c.getJSON(ctx, "/health", nil); err != nil {
		if rels, localErr := DartStructuralRelationships(repository, flowID); localErr == nil {
			c.Backend = "owned_dart_structural"
			return rels, nil
		}
		return nil, err
	}
	var tools any
	if err := c.getJSON(ctx, "/api/v1/tools", &tools); err != nil {
		if rels, localErr := DartStructuralRelationships(repository, flowID); localErr == nil {
			c.Backend = "owned_dart_structural"
			return rels, nil
		}
		return nil, err
	}
	shape := relationshipSchema(tools)
	if shape == "" {
		if rels, localErr := DartStructuralRelationships(repository, flowID); localErr == nil {
			c.Backend = "owned_dart_structural"
			return rels, nil
		}
		return nil, &Failure{"CODEGRAPH_INCOMPATIBLE", "CodeGraph analyze_code_relationships does not expose a supported argument schema"}
	}
	if shape == "cgc" && hasTool(tools, "add_code_to_graph") && hasTool(tools, "check_job_status") {
		if err := c.ensureIndexed(ctx, repository); err != nil {
			return nil, err
		}
	}
	var raw json.RawMessage
	args := map[string]any{}
	if shape == "codeflow" {
		args = map[string]any{"repository": repository, "entry_point": flowID, "contract_version": ContractVersion}
	} else {
		args = map[string]any{"query_type": "find_all_callees", "target": strings.TrimPrefix(flowID, "route:"), "context": repository, "repo_path": repository}
	}
	if err := c.postJSON(ctx, "/api/v1/tools/call", map[string]any{"name": "analyze_code_relationships", "arguments": args}, &raw); err != nil {
		return nil, err
	}
	rels, err := decodeRelationships(raw)
	if err != nil {
		return nil, &Failure{"CODEGRAPH_MALFORMED", err.Error()}
	}
	if len(rels) == 0 {
		if local, localErr := DartStructuralRelationships(repository, flowID); localErr == nil {
			c.Backend = "owned_dart_structural"
			return local, nil
		}
		return nil, &Failure{"CODEGRAPH_UNKNOWN", "CodeGraph returned no relationships for the selected route"}
	}
	if err := reanchor(repository, rels); shape == "cgc" && err != nil {
		return nil, &Failure{"CODEGRAPH_UNANCHORED", err.Error()}
	}
	c.Backend = "external_codegraphcontext"
	return rels, nil
}

// DomainSubgraph extracts the multi-hop causal graph for domain seeds.
// It queries CodeGraph or falls back to the owned structural domain extractor.
func (c *Client) DomainSubgraph(ctx context.Context, repository string, seeds []string, depth int) ([]Relationship, error) {
	if c == nil || c.BaseURL == "" {
		rels, err := DartStructuralDomainSubgraph(repository, seeds, depth)
		if err == nil {
			c.Backend = "owned_dart_structural"
		}
		return rels, err
	}
	if err := c.getJSON(ctx, "/health", nil); err != nil {
		if rels, localErr := DartStructuralDomainSubgraph(repository, seeds, depth); localErr == nil {
			c.Backend = "owned_dart_structural"
			return rels, nil
		}
		return nil, err
	}
	var tools any
	if err := c.getJSON(ctx, "/api/v1/tools", &tools); err != nil {
		if rels, localErr := DartStructuralDomainSubgraph(repository, seeds, depth); localErr == nil {
			c.Backend = "owned_dart_structural"
			return rels, nil
		}
		return nil, err
	}
	shape := relationshipSchema(tools)
	if shape == "" {
		if rels, localErr := DartStructuralDomainSubgraph(repository, seeds, depth); localErr == nil {
			c.Backend = "owned_dart_structural"
			return rels, nil
		}
		return nil, &Failure{"CODEGRAPH_INCOMPATIBLE", "CodeGraph analyze_code_relationships does not expose a supported argument schema"}
	}
	if shape == "cgc" && hasTool(tools, "add_code_to_graph") && hasTool(tools, "check_job_status") {
		if err := c.ensureIndexed(ctx, repository); err != nil {
			return nil, err
		}
	}
	allRels := []Relationship{}
	seen := map[string]bool{}
	for _, seed := range seeds {
		var raw json.RawMessage
		args := map[string]any{}
		if shape == "codeflow" {
			args = map[string]any{"repository": repository, "entry_point": seed, "contract_version": ContractVersion}
		} else {
			args = map[string]any{"query_type": "find_all_callees", "target": seed, "context": repository, "repo_path": repository}
		}
		if err := c.postJSON(ctx, "/api/v1/tools/call", map[string]any{"name": "analyze_code_relationships", "arguments": args}, &raw); err == nil {
			if rels, err := decodeRelationships(raw); err == nil {
				for _, r := range rels {
					k := fmt.Sprintf("%s:%s->%s:%s", r.From.Path, r.From.Symbol, r.To.Path, r.To.Symbol)
					if !seen[k] {
						seen[k] = true
						allRels = append(allRels, r)
					}
				}
			}
		}
	}
	if len(allRels) == 0 {
		if local, localErr := DartStructuralDomainSubgraph(repository, seeds, depth); localErr == nil {
			c.Backend = "owned_dart_structural"
			return local, nil
		}
		return nil, &Failure{"CODEGRAPH_UNKNOWN", "no domain relationships found"}
	}
	if err := reanchor(repository, allRels); shape == "cgc" && err != nil {
		return nil, &Failure{"CODEGRAPH_UNANCHORED", err.Error()}
	}
	c.Backend = "external_codegraphcontext"
	return allRels, nil
}

// ensureIndexed uses CodeGraphContext's public tool contracts when present.
// Listing is deliberately attempted first; indexing jobs are then polled under
// the caller deadline. A service without these tools remains query-compatible.
func (c *Client) ensureIndexed(ctx context.Context, repository string) error {
	var listed json.RawMessage
	if err := c.postJSON(ctx, "/api/v1/tools/call", map[string]any{"name": "list_indexed_repositories", "arguments": map[string]any{}}, &listed); err != nil {
		return err
	}
	if bytes.Contains(listed, []byte(repository)) {
		return nil
	}
	var started json.RawMessage
	if err := c.postJSON(ctx, "/api/v1/tools/call", map[string]any{"name": "add_code_to_graph", "arguments": map[string]any{"repo_path": repository}}, &started); err != nil {
		return err
	}
	job := findString(started, "job_id")
	if job == "" {
		return nil
	} // synchronous completion
	for {
		select {
		case <-ctx.Done():
			return &Failure{"CODEGRAPH_INDEXING", ctx.Err().Error()}
		default:
		}
		var status json.RawMessage
		if err := c.postJSON(ctx, "/api/v1/tools/call", map[string]any{"name": "check_job_status", "arguments": map[string]any{"job_id": job}}, &status); err != nil {
			return err
		}
		state := strings.ToLower(findString(status, "status"))
		if state == "completed" || state == "complete" || state == "success" {
			return nil
		}
		if state == "failed" || state == "cancelled" {
			return &Failure{"CODEGRAPH_INDEXING", "CodeGraph indexing job " + state}
		}
		select {
		case <-ctx.Done():
			return &Failure{"CODEGRAPH_INDEXING", ctx.Err().Error()}
		case <-time.After(100 * time.Millisecond):
		}
	}
}
func findString(raw json.RawMessage, key string) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	var find func(any) string
	find = func(x any) string {
		switch n := x.(type) {
		case map[string]any:
			if s, ok := n[key].(string); ok {
				return s
			}
			for _, v := range n {
				if s := find(v); s != "" {
					return s
				}
			}
		case []any:
			for _, v := range n {
				if s := find(v); s != "" {
					return s
				}
			}
		}
		return ""
	}
	return find(v)
}

// relationshipSchema validates actual argument properties, not a tool name.
// CodeGraphContext's public tool uses query_type/target; the earlier fixture
// bridge uses repository/entry_point. Anything else is explicitly incompatible.
func relationshipSchema(v any) string {
	for _, tool := range findTools(v) {
		if tool["name"] != "analyze_code_relationships" {
			continue
		}
		schema, _ := tool["inputSchema"].(map[string]any)
		if schema == nil {
			schema, _ = tool["input_schema"].(map[string]any)
		}
		if schema == nil {
			schema, _ = tool["parameters"].(map[string]any)
		}
		if schema == nil {
			return "codeflow"
		} // legacy captured fixture bridge
		props, _ := schema["properties"].(map[string]any)
		if props["query_type"] != nil && props["target"] != nil {
			return "cgc"
		}
		if props["repository"] != nil && props["entry_point"] != nil {
			return "codeflow"
		}
	}
	return ""
}

// CompatibleTools is the doctor-facing, schema-strict capability probe.
func CompatibleTools(v any) bool {
	shape := relationshipSchemaStrict(v)
	return shape == "cgc" || shape == "codeflow"
}
func relationshipSchemaStrict(v any) string {
	for _, tool := range findTools(v) {
		if tool["name"] != "analyze_code_relationships" {
			continue
		}
		schema, _ := tool["inputSchema"].(map[string]any)
		if schema == nil {
			schema, _ = tool["input_schema"].(map[string]any)
		}
		if schema == nil {
			schema, _ = tool["parameters"].(map[string]any)
		}
		props, _ := schema["properties"].(map[string]any)
		if props["query_type"] != nil && props["target"] != nil {
			return "cgc"
		}
		if props["repository"] != nil && props["entry_point"] != nil {
			return "codeflow"
		}
	}
	return ""
}
func findTools(v any) []map[string]any {
	var out []map[string]any
	switch x := v.(type) {
	case map[string]any:
		if _, ok := x["name"].(string); ok {
			out = append(out, x)
		}
		for _, k := range []string{"tools", "data", "result"} {
			out = append(out, findTools(x[k])...)
		}
	case []any:
		for _, item := range x {
			out = append(out, findTools(item)...)
		}
	}
	return out
}

// reanchor rejects graph prose and symbols unless they carry a current,
// repository-relative byte span. It fills only verifiable raw-byte hashes and
// revision from the current worktree; it never upgrades a guessed location.
func reanchor(repository string, rels []Relationship) error {
	for i := range rels {
		for _, a := range []*Anchor{&rels[i].From, &rels[i].To} {
			if a.Path == "" || a.Symbol == "" || a.ByteStart < 0 || a.ByteEnd <= a.ByteStart || filepath.IsAbs(a.Path) || strings.HasPrefix(filepath.ToSlash(a.Path), "../") {
				return fmt.Errorf("relationship lacks a re-anchorable source span")
			}
			bytes, err := os.ReadFile(filepath.Join(repository, filepath.FromSlash(a.Path)))
			if err != nil || a.ByteEnd > len(bytes) {
				return fmt.Errorf("relationship source span is unavailable")
			}
			if a.FileHash == "" {
				sum := sha256sum(bytes)
				a.FileHash = sum
			}
			if a.Revision == "" {
				a.Revision = gitRevision(repository)
			}
			if a.FileHash != sha256sum(bytes) || a.Revision == "" {
				return fmt.Errorf("relationship anchor is not current")
			}
		}
	}
	return nil
}
func sha256sum(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func gitRevision(repo string) string {
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (c *Client) getJSON(ctx context.Context, endpoint string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+endpoint, nil)
	resp, err := c.http().Do(req)
	if err != nil {
		return &Failure{"CODEGRAPH_UNAVAILABLE", err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &Failure{"CODEGRAPH_UNAVAILABLE", fmt.Sprintf("CodeGraph %s returned HTTP %d", endpoint, resp.StatusCode)}
	}
	if out != nil && json.NewDecoder(resp.Body).Decode(out) != nil {
		return &Failure{"CODEGRAPH_MALFORMED", "CodeGraph returned invalid JSON"}
	}
	return nil
}
func (c *Client) postJSON(ctx context.Context, endpoint string, body, out any) error {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+endpoint, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return &Failure{"CODEGRAPH_UNAVAILABLE", err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusAccepted {
		return &Failure{"CODEGRAPH_INDEXING", "CodeGraph accepted asynchronous indexing; retry after indexing completes"}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return &Failure{"CODEGRAPH_UNAVAILABLE", fmt.Sprintf("CodeGraph %s returned HTTP %d: %s", endpoint, resp.StatusCode, data)}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return &Failure{"CODEGRAPH_MALFORMED", "CodeGraph returned invalid JSON"}
	}
	return nil
}
func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}
func hasTool(v any, wanted string) bool {
	switch x := v.(type) {
	case map[string]any:
		if n, _ := x["name"].(string); n == wanted {
			return true
		}
		for _, k := range []string{"tools", "data", "result"} {
			if hasTool(x[k], wanted) {
				return true
			}
		}
	case []any:
		for _, v := range x {
			if hasTool(v, wanted) {
				return true
			}
		}
	}
	return false
}
func decodeRelationships(raw json.RawMessage) ([]Relationship, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	for {
		m, ok := v.(map[string]any)
		if !ok {
			break
		}
		next, ok := m["relationships"]
		if ok {
			v = next
			break
		}
		if n, ok := m["result"]; ok {
			v = n
			continue
		}
		if n, ok := m["data"]; ok {
			v = n
			continue
		}
		break
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out []Relationship
	if err = json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("relationships must be an array: %w", err)
	}
	return out, nil
}
