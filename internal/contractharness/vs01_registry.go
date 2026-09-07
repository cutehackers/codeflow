package contractharness

import (
	"encoding/json"
	"fmt"
)

// VS01ContractRegistryEntry records the producer, consumer, and fixture
// evidence for one canonical R2 VS-01 boundary.
type VS01ContractRegistryEntry struct {
	ID                     string
	SchemaID               string
	Producer               string
	Consumer               string
	ValidFixture           string
	InvalidPathFixture     string
	InvalidEpochFixture    string
	InvalidMutationFixture string
}

// VS01EvidenceRegistryID is the stable evidence registry identity from the
// approved VS-01 contract.
const VS01EvidenceRegistryID = "rflsc-r2-vs-01"

// VS01ContractRegistry is the single registry for the five required
// workspace contracts. Fixture paths are relative to schemas/fixtures.
var VS01ContractRegistry = []VS01ContractRegistryEntry{
	{
		ID:                     "rflsc.document-revision.v2",
		SchemaID:               BaseURL + "rflsc.document-revision.v2.schema.json",
		Producer:               "workspace.SnapshotEngine edit ingress",
		Consumer:               "workspace snapshot and analysis adapters",
		ValidFixture:           "rflsc.document-revision.v2/valid/revision.json",
		InvalidPathFixture:     "rflsc.document-revision.v2/invalid/zero-version.json",
		InvalidEpochFixture:    "rflsc.document-revision.v2/invalid/string-epoch.json",
		InvalidMutationFixture: "document-revision/invalid/missing-source.json",
	},
	{
		ID:                     "rflsc.workspace-snapshot.v2",
		SchemaID:               BaseURL + "rflsc.workspace-snapshot.v2.schema.json",
		Producer:               "workspace.SnapshotEngine snapshot commit",
		Consumer:               "workspace.SnapshotLease and analyzers",
		ValidFixture:           "rflsc.workspace-snapshot.v2/valid/snapshot.json",
		InvalidPathFixture:     "rflsc.workspace-snapshot.v2/invalid/missing-tree-id.json",
		InvalidEpochFixture:    "rflsc.workspace-snapshot.v2/invalid/string-epoch.json",
		InvalidMutationFixture: "workspace-snapshot/invalid/negative-sequence.json",
	},
	{
		ID:                     "rflsc.workspace-live-head.v2",
		SchemaID:               BaseURL + "rflsc.workspace-live-head.v2.schema.json",
		Producer:               "workspace.SnapshotEngine atomic live-head pointer",
		Consumer:               "workspace snapshot query",
		ValidFixture:           "rflsc.workspace-live-head.v2/valid/live-head.json",
		InvalidPathFixture:     "rflsc.workspace-live-head.v2/invalid/missing-snapshot.json",
		InvalidEpochFixture:    "rflsc.workspace-live-head.v2/invalid/string-epoch.json",
		InvalidMutationFixture: "rflsc.workspace-live-head.v2/invalid/missing-snapshot.json",
	},
	{
		ID:                     "rflsc.snapshot-lease.v1",
		SchemaID:               BaseURL + "rflsc.snapshot-lease.v1.schema.json",
		Producer:               "workspace.SnapshotEngine SnapshotVFS",
		Consumer:               "snapshot-backed parser and slicer",
		ValidFixture:           "rflsc.snapshot-lease.v1/valid/lease.json",
		InvalidPathFixture:     "rflsc.snapshot-lease.v1/invalid/missing-retentions.json",
		InvalidEpochFixture:    "rflsc.snapshot-lease.v1/invalid/string-epoch.json",
		InvalidMutationFixture: "rflsc.snapshot-lease.v1/invalid/missing-retentions.json",
	},
	{
		ID:                     "rflsc.workspace-epoch-transition.v2",
		SchemaID:               BaseURL + "rflsc.workspace-epoch-transition.v2.schema.json",
		Producer:               "workspace.SnapshotEngine epoch transition",
		Consumer:               "activity and currentness subscribers",
		ValidFixture:           "rflsc.workspace-epoch-transition.v2/valid/transition.json",
		InvalidPathFixture:     "rflsc.workspace-epoch-transition.v2/invalid/negative-epoch.json",
		InvalidEpochFixture:    "rflsc.workspace-epoch-transition.v2/invalid/string-epoch.json",
		InvalidMutationFixture: "rflsc.workspace-epoch-transition.v2/invalid/negative-epoch.json",
	},
}

// ValidateVS01RegistryEntry validates one registry payload and its semantic
// epoch transition invariant.
func ValidateVS01RegistryEntry(entry VS01ContractRegistryEntry, data []byte) error {
	if entry.ID == "" || entry.SchemaID == "" || entry.Producer == "" || entry.Consumer == "" || entry.ValidFixture == "" || entry.InvalidPathFixture == "" || entry.InvalidEpochFixture == "" || entry.InvalidMutationFixture == "" {
		return fmt.Errorf("VS-01 contract registry entry is incomplete: %+v", entry)
	}
	if err := Validate(entry.SchemaID, data); err != nil {
		return fmt.Errorf("%s schema violation: %w", entry.ID, err)
	}
	if entry.ID == "rflsc.workspace-epoch-transition.v2" {
		var transition struct {
			PreviousEpoch int64 `json:"previousEpoch"`
			NewEpoch      int64 `json:"newEpoch"`
		}
		if err := json.Unmarshal(data, &transition); err != nil {
			return fmt.Errorf("%s parse: %w", entry.ID, err)
		}
		if transition.NewEpoch <= transition.PreviousEpoch {
			return fmt.Errorf("%s newEpoch must advance beyond previousEpoch", entry.ID)
		}
	}
	return nil
}
