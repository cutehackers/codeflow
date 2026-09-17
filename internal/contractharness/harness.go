// Package contractharness compiles the CodeFlow v2 JSON Schema contracts and
// validates instances against them.
//
// CORE stages must consume contracts through Validate instead of hand-rolled
// checks so that schema drift is caught in one place (design §13, "계약" row).
package contractharness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"codeflow/schemas"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// BaseURL is the $id scheme every CodeFlow v2 contract uses
// ("https://codeflow.local/schemas/<name>.schema.json"). The URLs are logical
// identifiers only; this package resolves them to the schemas/ directory on
// disk, so no network access ever happens.
const BaseURL = "https://codeflow.local/schemas/"

// SchemaIDs lists all contracts. Cross-file $refs between them are
// resolved through the same loader.
var SchemaIDs = []string{
	BaseURL + "identity.schema.json",
	BaseURL + "candidate.schema.json",
	BaseURL + "sliced-payload.schema.json",
	BaseURL + "adapter-analysis.schema.json",
	BaseURL + "flowspec.schema.json",
	BaseURL + "session-artifact.schema.json",
	BaseURL + "adapter-protocol.schema.json",
	BaseURL + "core-artifact.schema.json",
	BaseURL + "layers-config.schema.json",
	BaseURL + "task-intent.schema.json",
	BaseURL + "task-view-query.schema.json",
	BaseURL + "semantic-map-ir.schema.json",
	BaseURL + "flow-view-projection.schema.json",
	BaseURL + "flow_sequence.schema.json",
	BaseURL + "document-revision.schema.json",
	BaseURL + "workspace-snapshot.schema.json",
	BaseURL + "change-batch.schema.json",
	BaseURL + "generation-proof-manifest.schema.json",
	BaseURL + "active-pointer.schema.json",
	BaseURL + "event-envelope.schema.json",
	BaseURL + "semantic-delta-ir.schema.json",
	BaseURL + "requirement-alignment.schema.json",
	BaseURL + "change-impact-graph.schema.json",
	BaseURL + "failure-path-trace.schema.json",
	BaseURL + "runtime-observation.schema.json",
	BaseURL + "model-proposal.schema.json",
	BaseURL + "semantic-approval.schema.json",
	BaseURL + "evidence-pack.schema.json",
	BaseURL + "rflsc.evidence-pack.v2.schema.json",
	BaseURL + "rflsc.model-host-request.v2.schema.json",
	BaseURL + "rflsc.model-host-response.v2.schema.json",
	BaseURL + "rflsc.semantic-proposal.v2.schema.json",
	BaseURL + "rflsc.enrichment-state.v2.schema.json",
	BaseURL + "rflsc.model-activation-disclosure.v1.schema.json",
	BaseURL + "rflsc.approval-command.v2.schema.json",
	BaseURL + "rflsc.approval-event.v2.schema.json",
	BaseURL + "rflsc.approval-aggregate.v2.schema.json",
	BaseURL + "rflsc.approval-idempotency-result.v1.schema.json",
	BaseURL + "rflsc.approval-outbox.v1.schema.json",
	BaseURL + "rflsc.approval-history.v1.schema.json",
	BaseURL + "domain-overview.schema.json",
	BaseURL + "representative-flow-catalog.schema.json",
	BaseURL + "rflsc.task-intent.v2.schema.json",
	BaseURL + "rflsc.feature-query.v2.schema.json",
	BaseURL + "rflsc.review-query.v2.schema.json",
	BaseURL + "rflsc.semantic-map-ir.v2.schema.json",
	BaseURL + "rflsc.semantic-delta-ir.v2.schema.json",
	BaseURL + "rflsc.requirement-alignment.v2.schema.json",
	BaseURL + "rflsc.flowview-projection.v2.schema.json",
	BaseURL + "rflsc.document-revision.v2.schema.json",
	BaseURL + "rflsc.workspace-snapshot.v2.schema.json",
	BaseURL + "rflsc.workspace-live-head.v2.schema.json",
	BaseURL + "rflsc.snapshot-lease.v1.schema.json",
	BaseURL + "rflsc.workspace-epoch-transition.v2.schema.json",
	BaseURL + "rflsc.analyzer-request.v2.schema.json",
	BaseURL + "rflsc.analyzer-result.v2.schema.json",
	BaseURL + "rflsc.analysis-read-set.v2.schema.json",
	BaseURL + "rflsc.observation-closure.v2.schema.json",
	BaseURL + "rflsc.evidence.v2.schema.json",
	BaseURL + "rflsc.adapter-capability-matrix.v1.schema.json",
	BaseURL + "rflsc.adapter-diagnostic.v2.schema.json",
	BaseURL + "rflsc.activity-state.v2.schema.json",
	BaseURL + "rflsc.publication-candidate.v2.schema.json",
	BaseURL + "rflsc.generation-proof-manifest.v2.schema.json",
	BaseURL + "rflsc.active-pointer.v2.schema.json",
	BaseURL + "rflsc.verified-gap.v2.schema.json",
	BaseURL + "rflsc.settlement.v2.schema.json",
	BaseURL + "rflsc.event-envelope.v2.schema.json",
	BaseURL + "rflsc.flowview-view-state.v2.schema.json",
	BaseURL + "rflsc.impact-query.v2.schema.json",
	BaseURL + "rflsc.change-impact-graph.v2.schema.json",
	BaseURL + "rflsc.impact-frontier.v1.schema.json",
	BaseURL + "rflsc.failure-query.v2.schema.json",
	BaseURL + "rflsc.failure-path-trace.v2.schema.json",
	BaseURL + "rflsc.runtime-observation.v2.schema.json",
	BaseURL + "rflsc.runtime-consent.v1.schema.json",
	BaseURL + "rflsc.runtime-isolation-result.v1.schema.json",
	BaseURL + "rflsc.onboarding-query.v2.schema.json",
	BaseURL + "rflsc.domain-overview.v2.schema.json",
	BaseURL + "rflsc.domain-candidate.v2.schema.json",
	BaseURL + "rflsc.representative-flow-catalog.v2.schema.json",
	BaseURL + "rflsc.onboarding-flow-drilldown.v2.schema.json",
	BaseURL + "rflsc.release-profile.v2.schema.json",
	BaseURL + "rflsc.scenario-manifest.v2.schema.json",
	BaseURL + "rflsc.execution-report.v2.schema.json",
	BaseURL + "rflsc.release-benchmark-report.v2.schema.json",
	BaseURL + "rflsc.release-capability-matrix.v2.schema.json",
}

// SchemasDir locates the repo's schemas/ directory relative to this source
// file when running in a source tree, or returns empty string if not found.
func SchemasDir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	dir := filepath.Dir(thisFile)
	for {
		candidate := filepath.Join(dir, "schemas", "identity.schema.json")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return filepath.Join(dir, "schemas")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// embedLoader maps BaseURL-prefixed $ids onto embedded schema files. Anything
// outside that namespace (or containing path traversal) is rejected.
type embedLoader struct {
	fsys fs.FS
}

func (l embedLoader) Load(url string) (any, error) {
	rest, ok := strings.CutPrefix(url, BaseURL)
	if !ok || rest == "" || strings.Contains(rest, "..") || strings.HasPrefix(rest, "/") {
		return nil, fmt.Errorf("contractharness: refusing to load non-contract schema url %q", url)
	}
	f, err := l.fsys.Open(filepath.ToSlash(rest))
	if err != nil {
		return nil, fmt.Errorf("contractharness: open %s: %w", rest, err)
	}
	defer f.Close()
	return jsonschema.UnmarshalJSON(f)
}

var (
	mu       sync.Mutex
	compiled = map[string]*jsonschema.Schema{}
)

func compile(id string) (*jsonschema.Schema, error) {
	mu.Lock()
	defer mu.Unlock()
	if sch, ok := compiled[id]; ok {
		return sch, nil
	}
	c := jsonschema.NewCompiler()
	c.UseLoader(embedLoader{fsys: schemas.FS})
	sch, err := c.Compile(id)
	if err != nil {
		return nil, err
	}
	compiled[id] = sch
	return sch, nil
}

// EnsureAllCompiled compiles all registered contracts (including cross-file
// $ref resolution against identity.schema.json). The golden-fixture test calls
// it first so a ref-resolution breakage fails loudly even if no fixture hits it.
func EnsureAllCompiled() error {
	for _, id := range SchemaIDs {
		if _, err := compile(id); err != nil {
			return fmt.Errorf("compile %s: %w", id, err)
		}
	}
	return nil
}

// Validate parses data as JSON and validates it against the schema document
// identified by schemaID (e.g. BaseURL+"flowspec.schema.json"). It returns a
// *jsonschema.ValidationError on violation; the error text includes the
// instance location of every failing keyword. CORE stages should call this at
// every contract boundary (harvest output, slice output, fusion input/output,
// MCP submissions, adapter JSON-RPC envelopes).
func Validate(schemaID string, data []byte) error {
	sch, err := compile(schemaID)
	if err != nil {
		return fmt.Errorf("compile %s: %w", schemaID, err)
	}
	var instance any
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&instance); err != nil {
		return fmt.Errorf("parse instance JSON: %w", err)
	}
	return sch.Validate(instance)
}

// SchemaIDForFixtureDir maps a fixture directory name (e.g. "flowspec") to its
// contract $id.
func SchemaIDForFixtureDir(name string) string {
	return BaseURL + name + ".schema.json"
}

// FixtureTree walks schemas/fixtures and validates every golden fixture:
// files under valid/ MUST pass, files under invalid/ MUST fail. The returned
// error names the offending fixture, the expected outcome, and the validator's
// reason, so a red CI run points straight at the drifted contract or the
// mis-labeled fixture.
func FixtureTree() ([]FixtureResult, error) {
	entries, err := fs.ReadDir(schemas.FixturesFS, "fixtures")
	if err != nil {
		return nil, fmt.Errorf("read fixtures root: %w", err)
	}
	var results []FixtureResult
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		schemaID := SchemaIDForFixtureDir(entry.Name())
		for _, kind := range []struct {
			dir      string
			mustPass bool
		}{{"valid", true}, {"invalid", false}} {
			dirPath := path.Join("fixtures", entry.Name(), kind.dir)
			files, err := fs.ReadDir(schemas.FixturesFS, dirPath)
			if err != nil {
				continue
			}
			for _, f := range files {
				if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
					continue
				}
				data, err := fs.ReadFile(schemas.FixturesFS, path.Join(dirPath, f.Name()))
				if err != nil {
					return nil, err
				}
				vErr := validateVS09FixtureData(schemaID, data)
				results = append(results, FixtureResult{
					Schema:   entry.Name(),
					Path:     filepath.ToSlash(filepath.Join(kind.dir, f.Name())),
					MustPass: kind.mustPass,
					Err:      vErr,
				})
			}
		}
	}
	return results, nil
}

// FixtureResult records one golden fixture validation outcome.
type FixtureResult struct {
	Schema   string
	Path     string
	MustPass bool
	Err      error // nil when the instance validated
}

// OK reports whether the outcome matches the fixture's expectation.
func (r FixtureResult) OK() bool {
	return (r.Err == nil) == r.MustPass
}
