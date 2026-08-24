// Package secret implements the single-gate secret scanner and redaction
// engine (design §16 R7, tickets 07/09).
//
// Every persistence, publishing, and MCP egress path passes through this gate.
package secret

import (
	"encoding/json"
	"regexp"
)

var secretPattern = regexp.MustCompile(`(?i)\b(?:api[_-]?key|secret|token|password)\s*[:=]\s*['"]?[^\s;'"]{3,}['"]?`)

// RedactionResult holds the sanitized string and the count of redactions performed.
type RedactionResult struct {
	Text  string
	Count int
}

// Redact replaces secret tokens matching standard key/token/password patterns
// with "***REDACTED***".
func Redact(input string) RedactionResult {
	count := 0
	replaced := secretPattern.ReplaceAllStringFunc(input, func(m string) string {
		count++
		return `***REDACTED***`
	})
	return RedactionResult{
		Text:  replaced,
		Count: count,
	}
}

// RedactJSON parses arbitrary JSON, recursively sanitizes all string fields,
// and re-encodes the clean JSON.
func RedactJSON(raw []byte) ([]byte, int, error) {
	var val any
	if err := json.Unmarshal(raw, &val); err != nil {
		// If not valid JSON, treat as raw text
		r := Redact(string(raw))
		return []byte(r.Text), r.Count, nil
	}
	totalCount := 0
	val = sanitizeValue(val, &totalCount)
	out, err := json.Marshal(val)
	if err != nil {
		return nil, 0, err
	}
	return out, totalCount, nil
}

func sanitizeValue(v any, count *int) any {
	switch val := v.(type) {
	case string:
		r := Redact(val)
		*count += r.Count
		return r.Text
	case map[string]any:
		res := make(map[string]any, len(val))
		for k, v2 := range val {
			res[k] = sanitizeValue(v2, count)
		}
		return res
	case []any:
		res := make([]any, len(val))
		for i, v2 := range val {
			res[i] = sanitizeValue(v2, count)
		}
		return res
	default:
		return val
	}
}
