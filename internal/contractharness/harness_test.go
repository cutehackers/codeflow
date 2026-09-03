package contractharness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"codeflow/schemas"
)

func TestEnsureAllCompiled(t *testing.T) {
	if err := EnsureAllCompiled(); err != nil {
		t.Fatalf("contract compilation failed: %v", err)
	}
}

func TestValidateGoldenFixtures(t *testing.T) {
	if err := EnsureAllCompiled(); err != nil {
		t.Fatalf("contract compilation failed: %v", err)
	}

	results, err := FixtureTree()
	if err != nil {
		t.Fatalf("walk fixture tree: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("no golden fixtures found under schemas/fixtures")
	}

	var (
		passedValid, passedInvalid int
		counts                     = map[string][2]int{} // schema -> {valid, invalid}
	)
	for _, r := range results {
		c := counts[r.Schema]
		if r.MustPass {
			c[0]++
		} else {
			c[1]++
		}
		counts[r.Schema] = c

		if r.OK() {
			if r.MustPass {
				passedValid++
			} else {
				passedInvalid++
			}
			continue
		}
		if r.MustPass {
			t.Errorf("[valid fixture failed — contract drift or bad fixture]\n  fixture: %s/%s\n  error:   %v",
				r.Schema, r.Path, r.Err)
		} else {
			t.Errorf("[invalid fixture unexpectedly VALIDATED — it no longer exercises its normative rejection]\n  fixture: %s/%s\n  hint:    the schema must still reject this document; if the contract legitimately changed, rewrite the fixture to target the new rejection",
				r.Schema, r.Path)
		}
	}

	for _, id := range SchemaIDs {
		// id is BaseURL + "<name>.schema.json"
		name := strings.TrimSuffix(strings.TrimPrefix(id, BaseURL), ".schema.json")
		c := counts[name]
		if c[0] == 0 || c[1] < 2 {
			t.Errorf("schema %q has insufficient golden coverage (valid=%d, invalid=%d); need >=1 valid and >=2 invalid fixtures each targeting a distinct normative rejection",
				name, c[0], c[1])
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "golden fixtures: %d valid + %d invalid = %d exercised\n", passedValid, passedInvalid, passedValid+passedInvalid)
	for _, s := range []string{"identity", "candidate", "sliced-payload", "adapter-analysis", "flowspec", "session-artifact", "adapter-protocol", "core-artifact", "layers-config", "task-intent", "task-view-query", "semantic-map-ir", "flow-view-projection", "document-revision", "workspace-snapshot", "change-batch"} {
		if c, ok := counts[s]; ok {
			fmt.Fprintf(&b, "  %-17s valid=%d invalid=%d\n", s, c[0], c[1])
		}
	}
	t.Log(b.String())
}

// TestValidateExportedContractBoundary pins the exported Validate API that
// future CORE stages consume: a good flowspec passes, the same document with a
// provenance field removed fails.
func TestValidateExportedContractBoundary(t *testing.T) {
	raw, err := fs.ReadFile(schemas.FixturesFS,
		"fixtures/flowspec/valid/email-signup-flowspec-with-stale-and-unknown.valid.json")
	if err != nil {
		t.Fatal(err)
	}
	id := SchemaIDForFixtureDir("flowspec")
	if err := Validate(id, raw); err != nil {
		t.Fatalf("expected flowspec to validate: %v", err)
	}

	var doc map[string]any
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	steps := doc["steps"].([]any)
	first := steps[0].(map[string]any)
	delete(first, "provenance")

	violated, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	err = Validate(id, violated)
	if err == nil {
		t.Fatal("expected flowspec step missing provenance to be rejected (normative: provenance REQUIRED)")
	}
	if !strings.Contains(fmt.Sprint(err), "provenance") && !strings.Contains(strings.ToLower(fmt.Sprint(err)), "required") {
		t.Errorf("rejection should mention the missing provenance requirement, got: %v", err)
	}
}
