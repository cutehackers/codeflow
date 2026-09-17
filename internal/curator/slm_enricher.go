package curator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// EnrichedNarrative represents a localized 1-line business narrative for a storyboard frame.
type EnrichedNarrative struct {
	FrameID   string `json:"frameId"`
	Narrative string `json:"narrative"`
	Status    string `json:"status"` // enriched | fallback | timed_out
}

// SLMConfig configures communication with the local external SLM runtime (e.g. Ollama).
type SLMConfig struct {
	Enabled   bool   `json:"enabled"`
	Endpoint  string `json:"endpoint"`  // default: http://localhost:11434/v1
	Model     string `json:"model"`     // default: qwen2.5-coder:1.5b
	TimeoutMs int    `json:"timeoutMs"` // default: 500
}

// SemanticEnricher enriches storyboard frames with micro-semantic business narratives.
type SemanticEnricher interface {
	EnrichStoryboard(ctx context.Context, frames []FlowFrame) ([]EnrichedNarrative, error)
	IsAvailable(ctx context.Context) bool
}

// ExternalHTTPEnricher communicates with an external OpenAI-compatible local model server.
type ExternalHTTPEnricher struct {
	config     SLMConfig
	httpClient *http.Client
}

// NewExternalHTTPEnricher constructs an ExternalHTTPEnricher with resilient defaults.
func NewExternalHTTPEnricher(cfg SLMConfig) *ExternalHTTPEnricher {
	if cfg.Endpoint == "" {
		cfg.Endpoint = "http://localhost:11434/v1"
	}
	cfg.Endpoint = strings.TrimSuffix(cfg.Endpoint, "/")
	if cfg.Model == "" {
		cfg.Model = "qwen2.5-coder:1.5b"
	}
	if cfg.TimeoutMs <= 0 {
		cfg.TimeoutMs = 500
	}

	return &ExternalHTTPEnricher{
		config: cfg,
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.TimeoutMs) * time.Millisecond,
		},
	}
}

// IsAvailable checks whether the external model runtime is enabled.
func (e *ExternalHTTPEnricher) IsAvailable(ctx context.Context) bool {
	return e.config.Enabled && e.config.Endpoint != ""
}

var markdownCodeBlockRe = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")

// ExtractJSONPayload strips markdown code blocks and extracts raw JSON from model text.
func ExtractJSONPayload(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if match := markdownCodeBlockRe.FindStringSubmatch(trimmed); len(match) > 1 {
		trimmed = strings.TrimSpace(match[1])
	}

	start := strings.IndexAny(trimmed, "[{")
	if start == -1 {
		return nil, errors.New("no json payload detected in response")
	}

	endArray := strings.LastIndex(trimmed, "]")
	endObject := strings.LastIndex(trimmed, "}")
	end := endArray
	if endObject > end {
		end = endObject
	}
	if end == -1 || end < start {
		return nil, errors.New("unbalanced json payload in response")
	}

	return []byte(trimmed[start : end+1]), nil
}

// EnrichStoryboard generates localized business narratives using verified facts.
// Falls back silently on connection refused, timeouts (>500ms), or malformed output.
func (e *ExternalHTTPEnricher) EnrichStoryboard(ctx context.Context, frames []FlowFrame) ([]EnrichedNarrative, error) {
	fallback := make([]EnrichedNarrative, len(frames))
	for i, f := range frames {
		fallback[i] = EnrichedNarrative{
			FrameID:   f.FrameID,
			Narrative: f.Title,
			Status:    "fallback",
		}
	}

	if !e.config.Enabled || len(frames) == 0 {
		return fallback, nil
	}

	timeout := time.Duration(e.config.TimeoutMs) * time.Millisecond
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var promptLines []string
	for _, f := range frames {
		line := fmt.Sprintf("관문: %s, 역할: %s, 심볼: %s", f.FrameID, f.Role, f.Title)
		if f.Condition != nil && *f.Condition != "" {
			line += fmt.Sprintf(", 조건: %s", *f.Condition)
		}
		promptLines = append(promptLines, line)
	}

	reqBody := map[string]any{
		"model": e.config.Model,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "당신은 코드의 비즈니스 실행 의미를 1줄로 요약하는 비즈니스 분석가입니다. 환각을 방지하기 위해 제공된 정적 호출 사실만 참조하여 JSON 배열 [{\"frameId\": \"...\", \"narrative\": \"...\"}]로만 응답하십시오.",
			},
			{
				"role":    "user",
				"content": strings.Join(promptLines, "\n"),
			},
		},
		"temperature": 0.1,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fallback, nil
	}

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, e.config.Endpoint+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return fallback, nil
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || reqCtx.Err() == context.DeadlineExceeded {
			for i := range fallback {
				fallback[i].Status = "timed_out"
			}
		}
		return fallback, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fallback, nil
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil || len(chatResp.Choices) == 0 {
		return fallback, nil
	}

	rawContent := chatResp.Choices[0].Message.Content
	jsonBytes, err := ExtractJSONPayload(rawContent)
	if err != nil {
		return fallback, nil
	}

	var parsed []EnrichedNarrative
	if err := json.Unmarshal(jsonBytes, &parsed); err != nil || len(parsed) == 0 {
		return fallback, nil
	}

	for i := range parsed {
		parsed[i].Status = "enriched"
	}

	return parsed, nil
}
