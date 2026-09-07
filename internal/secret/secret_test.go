package secret_test

import (
	"strings"
	"testing"

	"codeflow/internal/secret"
)

func TestRedact(t *testing.T) {
	cases := []struct {
		in        string
		wantCount int
		contains  string
	}{
		{
			in:        `apiKey: "sk-1234567890abcdef"`,
			wantCount: 1,
			contains:  `***REDACTED***`,
		},
		{
			in:        `password = 'mySuperSecretPassword!' and token: "bearer-xyz"`,
			wantCount: 2,
			contains:  `***REDACTED***`,
		},
		{
			in:        `regular text without sensitive credentials`,
			wantCount: 0,
			contains:  `regular text`,
		},
	}

	for _, tc := range cases {
		res := secret.Redact(tc.in)
		if res.Count != tc.wantCount {
			t.Errorf("Redact(%q) count = %d, want %d", tc.in, res.Count, tc.wantCount)
		}
		if tc.wantCount > 0 && res.Text == tc.in {
			t.Errorf("Redact(%q) did not sanitize text", tc.in)
		}
	}
}

func TestRedactJSON(t *testing.T) {
	raw := []byte(`{"title":"Login Flow","secret_data":"api_key = 'secret_token_123'","steps":[{"name":"submit","param":"password: 'pwd'"}]}`)
	clean, count, err := secret.RedactJSON(raw)
	if err != nil {
		t.Fatalf("RedactJSON failed: %v", err)
	}
	if count != 2 {
		t.Errorf("RedactJSON count = %d, want 2", count)
	}
	if string(clean) == string(raw) {
		t.Errorf("RedactJSON returned unchanged JSON")
	}
}

func TestRedactJSONSanitizesSecretKeysBeforeClip(t *testing.T) {
	secretValue := strings.Repeat("sensitive-value-", 80)
	raw := []byte(`{"diagnostic":"safe-prefix","databasePassword":"` + secretValue + `","safe":"visible"}`)
	clean, count, err := secret.RedactJSON(raw)
	if err != nil {
		t.Fatalf("RedactJSON failed: %v", err)
	}
	if count == 0 || strings.Contains(string(clean), secretValue[:24]) {
		t.Fatalf("long keyed secret was not fully redacted: count=%d clean=%s", count, clean)
	}
}

func TestRedactAndClipSanitizesStructuredKeysBeforeClip(t *testing.T) {
	secretValue := strings.Repeat("long-secret-value-", 80)
	value := map[string]any{
		"databasePassword": secretValue,
		"safe":             strings.Repeat("diagnostic-", 80),
	}
	clean, ok := secret.RedactAndClip(value, 32, 4).(map[string]any)
	if !ok {
		t.Fatal("RedactAndClip did not preserve object shape")
	}
	if got, _ := clean["databasePassword"].(string); got != "***REDACTED***" {
		t.Fatalf("keyed secret = %q, want redacted marker", got)
	}
	if got, _ := clean["safe"].(string); len(got) != 32 {
		t.Fatalf("safe diagnostic length = %d, want 32", len(got))
	}
}

func TestRedactMalformedJSONQuotedKeyBeforeClip(t *testing.T) {
	secretValue := strings.Repeat("secret-value-", 200)
	raw := []byte(`{"databasePassword":"` + secretValue + `","x-api-key":"` + secretValue + `","clientSecret":"` + secretValue)
	clean, count, err := secret.RedactJSON(raw)
	if err != nil {
		t.Fatalf("RedactJSON failed: %v", err)
	}
	if count < 3 || strings.Contains(string(clean), secretValue[:32]) {
		t.Fatalf("malformed quoted-key secret leaked: count=%d clean prefix=%q", count, string(clean)[:min(len(clean), 80)])
	}
}

func TestRedactAndClipNestedDiagnosticsBeforeBound(t *testing.T) {
	secretValue := strings.Repeat("nested-secret-", 200)
	value := map[string]any{
		"outer": []any{
			map[string]any{"clientSecret": secretValue},
			map[string]any{"safe": strings.Repeat("diagnostic-", 100)},
		},
	}
	clean, ok := secret.RedactAndClip(value, 48, 8).(map[string]any)
	if !ok {
		t.Fatal("RedactAndClip did not preserve nested object shape")
	}
	items, ok := clean["outer"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("nested diagnostics = %#v", clean)
	}
	secretObject := items[0].(map[string]any)
	if secretObject["clientSecret"] != "***REDACTED***" {
		t.Fatalf("nested clientSecret = %#v", secretObject["clientSecret"])
	}
	safeObject := items[1].(map[string]any)
	if got := safeObject["safe"].(string); len(got) != 48 {
		t.Fatalf("nested safe diagnostic length = %d, want 48", len(got))
	}
}

func TestRedactAndClipPointerStructuredDiagnostics(t *testing.T) {
	secretValue := strings.Repeat("pointer-secret-", 200)
	detail := &struct {
		ClientSecret string `json:"clientSecret"`
		Safe         string `json:"safe"`
	}{
		ClientSecret: secretValue,
		Safe:         strings.Repeat("diagnostic-", 100),
	}
	clean, ok := secret.RedactAndClip(detail, 32, 4).(map[string]any)
	if !ok {
		t.Fatalf("pointer structured detail = %#v, want normalized object", clean)
	}
	if got := clean["clientSecret"]; got != "***REDACTED***" {
		t.Fatalf("pointer clientSecret = %#v, want redacted marker", got)
	}
	if got, _ := clean["safe"].(string); len(got) != 32 {
		t.Fatalf("pointer safe diagnostic length = %d, want 32", len(got))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
