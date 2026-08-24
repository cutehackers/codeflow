package secret_test

import (
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
