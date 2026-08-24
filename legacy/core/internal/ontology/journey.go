package ontology

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const businessJourneysFile = "business-journeys.v1.json"

var journeyID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// JourneySegment is a reviewed reference to one deterministic scenario. It
// intentionally stores no generated code facts: the published FlowIR remains
// the only authority for causal evidence and trust.
type JourneySegment struct {
	FlowID     string `json:"flow_id"`
	ScenarioID string `json:"scenario_id"`
}

// BusinessJourney is an explicitly approved business-level reading of one or
// more source-backed scenarios. ID is reviewer-owned rather than derived from
// prose, so copy edits do not silently create a second journey.
type BusinessJourney struct {
	ID       string           `json:"id"`
	Title    string           `json:"title"`
	Outcome  string           `json:"outcome,omitempty"`
	Segments []JourneySegment `json:"segments"`
	Status   string           `json:"status"`
}

type businessJourneyFile struct {
	Version  string            `json:"version"`
	Journeys []BusinessJourney `json:"journeys"`
}

func LoadBusinessJourneys(repo string) ([]BusinessJourney, error) {
	path := filepath.Join(repo, ".codeflow", "knowledge", businessJourneysFile)
	bytes, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []BusinessJourney{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read business journeys: %w", err)
	}
	var stored businessJourneyFile
	if err := json.Unmarshal(bytes, &stored); err != nil {
		return nil, fmt.Errorf("read business journeys: %w", err)
	}
	if stored.Version != "1" {
		return nil, fmt.Errorf("unsupported business journeys version %q", stored.Version)
	}
	seen := map[string]bool{}
	for _, journey := range stored.Journeys {
		if err := validateBusinessJourney(journey); err != nil {
			return nil, err
		}
		if seen[journey.ID] {
			return nil, fmt.Errorf("business journey id %q is duplicated", journey.ID)
		}
		seen[journey.ID] = true
	}
	sort.Slice(stored.Journeys, func(i, j int) bool { return stored.Journeys[i].ID < stored.Journeys[j].ID })
	return stored.Journeys, nil
}

// SaveBusinessJourney is the explicit local approval boundary for business
// meaning. Core validates every segment against its current workspace before
// calling this function.
func SaveBusinessJourney(repo string, journey BusinessJourney) (BusinessJourney, error) {
	journey.ID = strings.TrimSpace(journey.ID)
	journey.Title = normalizeJourneyCopy(journey.Title)
	journey.Outcome = normalizeJourneyCopy(journey.Outcome)
	journey.Status = "confirmed"
	if err := validateBusinessJourney(journey); err != nil {
		return BusinessJourney{}, err
	}
	journeys, err := LoadBusinessJourneys(repo)
	if err != nil {
		return BusinessJourney{}, err
	}
	found := false
	for i := range journeys {
		if journeys[i].ID == journey.ID {
			journeys[i], found = journey, true
		}
	}
	if !found {
		journeys = append(journeys, journey)
	}
	sort.Slice(journeys, func(i, j int) bool { return journeys[i].ID < journeys[j].ID })
	dir := filepath.Join(repo, ".codeflow", "knowledge")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return BusinessJourney{}, err
	}
	bytes, err := json.MarshalIndent(businessJourneyFile{Version: "1", Journeys: journeys}, "", "  ")
	if err != nil {
		return BusinessJourney{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, businessJourneysFile), append(bytes, '\n'), 0644); err != nil {
		return BusinessJourney{}, err
	}
	return journey, nil
}

func normalizeJourneyCopy(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func validateBusinessJourney(journey BusinessJourney) error {
	if !journeyID.MatchString(journey.ID) || journey.Title == "" || journey.Status != "confirmed" {
		return fmt.Errorf("business journey requires a lowercase id, title, and confirmed status")
	}
	if len([]rune(journey.Title)) > 140 || len([]rune(journey.Outcome)) > 200 || secret.MatchString(journey.Title) || secret.MatchString(journey.Outcome) {
		return fmt.Errorf("business journey copy is too long or contains secret-like material")
	}
	if len(journey.Segments) == 0 || len(journey.Segments) > 20 {
		return fmt.Errorf("business journey requires between 1 and 20 scenario segments")
	}
	seen := map[string]bool{}
	for _, segment := range journey.Segments {
		key := segment.FlowID + "\x00" + segment.ScenarioID
		if segment.FlowID == "" || segment.ScenarioID == "" || seen[key] {
			return fmt.Errorf("business journey segments require unique flow_id and scenario_id values")
		}
		seen[key] = true
	}
	return nil
}
