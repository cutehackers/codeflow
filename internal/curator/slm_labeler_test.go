package curator_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codeflow/internal/curator"
)

func TestSLMLabeler_HappyPath(t *testing.T) {
	// Mock OpenAI-compatible Ollama server returning valid JSON
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]string{
						"content": `[{"frameID": "frame-01", "text": "결제 요청 승인 처리"}]`,
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	labeler := curator.NewSLMLabeler(curator.SLMConfig{
		Enabled:   true,
		Endpoint:  srv.URL + "/v1",
		TimeoutMs: 500,
	})

	frames := []curator.FlowSequenceFrame{
		{
			FrameID: "frame-01",
			Role:    "effect",
			Title:   "ProcessPayment",
		},
	}

	texts, err := labeler.LabelFlowSequence(context.Background(), frames)
	if err != nil {
		t.Fatalf("LabelFlowSequence failed: %v", err)
	}
	if len(texts) != 1 {
		t.Fatalf("expected 1 text, got %d", len(texts))
	}
	if texts[0].Status != "proposed" {
		t.Errorf("expected status proposed, got: %s", texts[0].Status)
	}
	if texts[0].Text != "결제 요청 승인 처리" {
		t.Errorf("unexpected text: %s", texts[0].Text)
	}
}

func TestSLMLabeler_MarkdownCodeBlockExtraction(t *testing.T) {
	// Mock returning JSON inside markdown ```json code block
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]string{
						"content": "Here is the response:\n```json\n[\n  {\"frameID\": \"f1\", \"text\": \"재고 수량 유효성 검증\"}\n]\n```\nHope this helps!",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	labeler := curator.NewSLMLabeler(curator.SLMConfig{
		Enabled:   true,
		Endpoint:  srv.URL + "/v1",
		TimeoutMs: 500,
	})

	frames := []curator.FlowSequenceFrame{
		{
			FrameID: "f1",
			Role:    "decision",
			Title:   "ValidateStock",
		},
	}

	texts, err := labeler.LabelFlowSequence(context.Background(), frames)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(texts) != 1 || texts[0].Status != "proposed" {
		t.Fatalf("expected 1 proposed text, got: %+v", texts)
	}
	if texts[0].Text != "재고 수량 유효성 검증" {
		t.Errorf("unexpected text: %s", texts[0].Text)
	}
}

func TestSLMLabeler_TimeoutSilentFallback(t *testing.T) {
	// Mock server that sleeps longer than TimeoutMs (100ms timeout, 300ms sleep)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	labeler := curator.NewSLMLabeler(curator.SLMConfig{
		Enabled:   true,
		Endpoint:  srv.URL + "/v1",
		TimeoutMs: 50, // 50ms strict budget
	})

	frames := []curator.FlowSequenceFrame{
		{
			FrameID: "f1",
			Role:    "entry",
			Title:   "OrderCheckout",
		},
	}

	t0 := time.Now()
	texts, err := labeler.LabelFlowSequence(context.Background(), frames)
	elapsed := time.Since(t0)

	if err != nil {
		t.Fatalf("expected silent fallback (no error), got: %v", err)
	}
	if elapsed > 150*time.Millisecond {
		t.Errorf("timeout took too long: %v (budget 50ms)", elapsed)
	}
	if len(texts) != 1 {
		t.Fatalf("expected 1 fallback text, got %d", len(texts))
	}
	if texts[0].Status != "timed_out" && texts[0].Status != "fallback" {
		t.Errorf("expected timed_out or fallback status, got: %s", texts[0].Status)
	}
	if texts[0].Text != "OrderCheckout" {
		t.Errorf("expected fallback to original title, got: %s", texts[0].Text)
	}
}

func TestSLMLabeler_DisabledNoNetworkCall(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	labeler := curator.NewSLMLabeler(curator.SLMConfig{
		Enabled:  false, // Disabled
		Endpoint: srv.URL,
	})

	frames := []curator.FlowSequenceFrame{
		{FrameID: "f1", Title: "StepOne"},
	}

	texts, err := labeler.LabelFlowSequence(context.Background(), frames)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("expected no HTTP call when SLM is disabled")
	}
	if len(texts) != 1 || texts[0].Status != "fallback" {
		t.Errorf("unexpected texts: %+v", texts)
	}
}

func TestExtractJSONPayload_Variations(t *testing.T) {
	tests := []struct {
		input       string
		expected    string
		shouldError bool
	}{
		{
			input:    `[{"frameID": "1"}]`,
			expected: `[{"frameID": "1"}]`,
		},
		{
			input:    "```json\n[{\"frameID\": \"2\"}]\n```",
			expected: `[{"frameID": "2"}]`,
		},
		{
			input:    "Leading text ```[{\"frameID\": \"3\"}]``` Trailing text",
			expected: `[{"frameID": "3"}]`,
		},
		{
			input:       "No json at all here",
			shouldError: true,
		},
	}

	for _, tc := range tests {
		extracted, err := curator.ExtractJSONPayload(tc.input)
		if tc.shouldError {
			if err == nil {
				t.Errorf("expected error for %q, got: %s", tc.input, string(extracted))
			}
			continue
		}
		if err != nil {
			t.Errorf("unexpected error for %q: %v", tc.input, err)
			continue
		}
		if strings.TrimSpace(string(extracted)) != tc.expected {
			t.Errorf("expected %q, got %q", tc.expected, string(extracted))
		}
	}
}
