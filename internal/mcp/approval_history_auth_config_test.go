package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPApprovalHistoryRequireTokenUsesCanonicalBoundedTokenContract(t *testing.T) {
	validToken := strings.Repeat("é", 256)
	server, err := NewServer(Config{
		RepoRoot:     t.TempDir(),
		RequireToken: true,
		AuthToken:    validToken,
	})
	if err != nil {
		t.Fatalf("NewServer rejected a valid 256-rune token: %v", err)
	}
	args, err := json.Marshal(map[string]string{
		"proposalId":     "missing-proposal",
		"evidencePackId": "missing-pack",
		"token":          validToken,
	})
	if err != nil {
		_ = server.Close()
		t.Fatalf("marshal history arguments: %v", err)
	}
	raw := serveMCPApprovalHistoryRequest(t, server, mcpApprovalHistoryToolCall("get_semantic_approval_history", args))
	_ = server.Close()
	requireMCPApprovalHistoryError(t, raw, "approval_unavailable")

	cases := []struct {
		name  string
		token string
	}{
		{name: "257 runes", token: strings.Repeat("é", 257)},
		{name: "invalid UTF-8", token: string([]byte{0xff})},
		{name: "empty", token: ""},
		{name: "whitespace", token: " \t\n"},
		{name: "leading whitespace", token: " token"},
		{name: "trailing whitespace", token: "token "},
		{name: "escaped control", token: "token\x1b"},
		{name: "interior C1 control", token: "token\u0080value"},
		{name: "interior Unicode line control", token: "token\u0085value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, err := NewServer(Config{
				RepoRoot:     t.TempDir(),
				RequireToken: true,
				AuthToken:    tc.token,
			})
			if err == nil {
				_ = server.Close()
				t.Fatalf("NewServer accepted an invalid configured token")
			}
			if err.Error() != "auth token configuration is invalid" {
				t.Fatalf("NewServer returned an unbounded configuration error")
			}
		})
	}

	server, err = NewServer(Config{
		RepoRoot:     t.TempDir(),
		RequireToken: false,
		AuthToken:    strings.Repeat("é", 257),
	})
	if err != nil {
		t.Fatalf("RequireToken=false changed existing configuration semantics: %v", err)
	}
	_ = server.Close()
}
