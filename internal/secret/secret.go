// Package secret implements the single-gate secret scanner and redaction
// engine (design §16 R7, tickets 07/09).
//
// Every persistence, publishing, and MCP egress path passes through this gate.
package secret

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
)

var secretKeyPattern = regexp.MustCompile("(?i)(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret)")

var secretPattern = regexp.MustCompile(`(?i)(?:\b(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret)\b|[A-Za-z][A-Za-z0-9]*(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret))\s*[:=]\s*['"]?[^\s;'"}]+['"]?`)

// quotedSecretPattern covers JSON-like diagnostics that are incomplete or
// otherwise malformed, so a quoted key and a long value are redacted before
// any diagnostic clipping. Valid JSON takes the recursive RedactJSON path.
var quotedSecretPattern = regexp.MustCompile(`(?is)"(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret)"\s*:\s*(?:"[^"\\]*(?:\\.[^"\\]*)*"?|[^,\s}\]]+)`)

// broadQuotedSecretPattern also recognizes prefixed and suffixed credential
// names such as databasePassword, x-api-key, and clientSecret.
var broadQuotedSecretPattern = regexp.MustCompile("(?is)\\\"(?:(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret)[A-Za-z0-9_-]*|[A-Za-z][A-Za-z0-9_-]*(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret)[A-Za-z0-9_-]*)\\\"\\s*:\\s*(?:\\\"[^\\\"\\\\]*(?:\\\\.[^\\\"\\\\]*)*\\\"?|[^,\\s}\\]]+)")

// RedactionResult holds the sanitized string and the count of redactions performed.
type RedactionResult struct {
	Text  string
	Count int
}

// Redact replaces secret tokens matching standard key/token/password patterns
// with "***REDACTED***".
func Redact(input string) RedactionResult {
	count := 0
	redactedJSON := broadQuotedSecretPattern.ReplaceAllStringFunc(input, func(m string) string {
		count++
		return `***REDACTED***`
	})
	redactedJSON = quotedSecretPattern.ReplaceAllStringFunc(redactedJSON, func(m string) string {
		count++
		return `***REDACTED***`
	})
	replaced := secretPattern.ReplaceAllStringFunc(redactedJSON, func(m string) string {
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

// RedactAndClip recursively redacts sensitive keys and values before applying
// bounded diagnostic clipping. It is intended for structured error details,
// where formatting a map to text first would lose the key-aware redaction
// guarantee.
func RedactAndClip(value any, maxStringBytes, maxItems int) any {
	count := 0
	redacted := sanitizeValue(value, &count)
	return clipValue(redacted, maxStringBytes, maxItems)
}

func clipValue(value any, maxStringBytes, maxItems int) any {
	switch typed := value.(type) {
	case string:
		if maxStringBytes > 0 && len(typed) > maxStringBytes {
			return typed[:maxStringBytes]
		}
		return typed
	case []any:
		limit := len(typed)
		if maxItems > 0 && limit > maxItems {
			limit = maxItems
		}
		out := make([]any, limit)
		for i := 0; i < limit; i++ {
			out[i] = clipValue(typed[i], maxStringBytes, maxItems)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = clipValue(item, maxStringBytes, maxItems)
		}
		return out
	default:
		return value
	}
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
			if sensitiveKey(k) {
				*count += 1
				res[k] = "***REDACTED***"
				continue
			}
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
		// Normalize typed maps, slices, arrays, and structs through the JSON
		// representation so locally constructed diagnostics receive the same
		// key-aware treatment as decoded JSON values.
		rv := reflect.ValueOf(v)
		if rv.IsValid() {
			// Error details are commonly wrapped in an interface or pointer.
			// Unwrap those values before inspecting their JSON shape so a pointer
			// to a struct cannot bypass key-aware redaction. Nil wrappers are
			// retained as nil diagnostics.
			for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
				if rv.IsNil() {
					return nil
				}
				if !rv.Elem().CanInterface() {
					break
				}
				rv = rv.Elem()
			}
			switch rv.Kind() {
			case reflect.Map, reflect.Slice, reflect.Array, reflect.Struct:
				raw, err := json.Marshal(v)
				if err == nil {
					var normalized any
					if json.Unmarshal(raw, &normalized) == nil {
						return sanitizeValue(normalized, count)
					}
				}
			}
		}
		return val
	}
}

// sensitiveKey recognizes JSON field names that identify credentials even
// when a producer adds a prefix/suffix such as "databasePassword" or
// "x-api-key". Key matching happens before descending into values, so a
// secret longer than the diagnostic clip boundary cannot leak a fragment.
func sensitiveKey(raw string) bool {
	key := strings.ToLower(strings.TrimSpace(raw))
	if key == "" || !secretKeyPattern.MatchString(key) {
		return false
	}
	compact := strings.NewReplacer("-", "", "_", "", " ", "").Replace(key)
	for _, marker := range []string{
		"apikey", "secret", "token", "password", "credential", "authorization",
		"privatekey", "accesskey", "accesstoken", "clientsecret",
	} {
		if compact == marker || strings.HasPrefix(compact, marker) || strings.HasSuffix(compact, marker) {
			return true
		}
	}
	return false
}
