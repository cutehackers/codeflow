package pin

import (
	"strings"
	"testing"
)

func TestDefaultResolvesEmbeddedDartPin(t *testing.T) {
	reg, err := Default()
	if err != nil {
		t.Fatalf("Default() error = %v", err)
	}
	version, err := reg.Resolve("dart")
	if err != nil {
		t.Fatalf("Resolve(dart) error = %v", err)
	}
	if version == "" {
		t.Error("embedded dart pin must be a concrete version")
	}
	if min, err := reg.Min("dart"); err != nil || min == "" {
		t.Errorf("Min(dart) = %q, %v; want non-empty", min, err)
	}
	pins := reg.Pins()
	if _, ok := pins["dart"]; !ok {
		t.Errorf("Pins() = %v, want to include dart", pins)
	}
	if got := reg.Names(); len(got) != 1 || got[0] != "dart" {
		t.Errorf("Names() = %v, want [dart]", got)
	}
}

func TestParseIsTheOverrideHookForTests(t *testing.T) {
	reg, err := Parse([]byte(`{
		"schemaVersion": "2.0",
		"adapters": {
			"kotlin": {"pinned": "0.2.0", "min": "0.1.0"}
		}
	}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if version, err := reg.Resolve("kotlin"); err != nil || version != "0.2.0" {
		t.Errorf("Resolve(kotlin) = %q, %v; want 0.2.0", version, err)
	}
	if _, err := reg.Resolve("dart"); err == nil {
		t.Error("override registry should not know the embedded dart pin")
	}
}

func TestParseRejectsBadData(t *testing.T) {
	tests := map[string]string{
		"malformed json":     `{not json`,
		"wrong schema":       `{"schemaVersion":"1","adapters":{"dart":{"pinned":"0.1.0","min":"0.1.0"}}}`,
		"no adapters":        `{"schemaVersion":"2.0","adapters":{}}`,
		"missing pinned":     `{"schemaVersion":"2.0","adapters":{"dart":{"min":"0.1.0"}}}`,
		"missing min":        `{"schemaVersion":"2.0","adapters":{"dart":{"pinned":"0.1.0"}}}`,
		"empty adapter name": `{"schemaVersion":"2.0","adapters":{"":{"pinned":"0.1.0","min":"0.1.0"}}}`,
	}
	for name, data := range tests {
		if _, err := Parse([]byte(data)); err == nil {
			t.Errorf("%s: Parse should reject %s", name, data)
		}
	}
}

func TestResolveUnknownAdapterError(t *testing.T) {
	reg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.Resolve("cobol")
	if err == nil || !strings.Contains(err.Error(), "cobol") {
		t.Errorf("Resolve(cobol) error = %v, want unknown-adapter error naming it", err)
	}
}
