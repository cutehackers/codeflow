package core

import (
	"net/url"

	"codeflow/core/internal/flowir"
	"codeflow/core/internal/ontology"
)

// journeyNavigation is a presentation projection of reviewer-approved journey
// definitions against one exact workspace publication. It is deliberately not
// persisted with FlowIR: source facts remain the authority for every status.
type journeyNavigation struct {
	Journeys []journeyNavigationItem
	Selected *journeyNavigationItem
}

type journeyNavigationItem struct {
	ID, Title, Outcome string
	Status             flowir.Status
	Segments           []journeySegmentNavigation
	Selected, Stale    bool
}

type journeySegmentNavigation struct {
	FlowID, ScenarioID, Title, URL string
	Status                         flowir.Status
	Steps, Unknowns                int
}

func buildJourneyNavigation(workspace workspaceDocument, labels map[string]ontology.DomainLabel, journeys []ontology.BusinessJourney, selected string) journeyNavigation {
	items := make([]journeyNavigationItem, 0, len(journeys))
	for _, journey := range journeys {
		item := journeyNavigationItem{ID: journey.ID, Title: journey.Title, Outcome: journey.Outcome, Status: flowir.Observed}
		for _, segment := range journey.Segments {
			document, scenario, ok := journeyScenario(workspace, segment)
			if !ok {
				item.Stale, item.Status = true, flowir.Unknown
				item.Segments = append(item.Segments, journeySegmentNavigation{FlowID: segment.FlowID, ScenarioID: segment.ScenarioID, Title: "현재 코드에서 경로를 다시 연결해야 합니다.", Status: flowir.Unknown})
				continue
			}
			unknowns := scenarioUnknowns(document, scenario)
			item.Segments = append(item.Segments, journeySegmentNavigation{FlowID: segment.FlowID, ScenarioID: segment.ScenarioID, Title: scenarioTitle(document, scenario, labels), URL: "/?journey=" + url.QueryEscape(journey.ID) + "&flow=" + url.QueryEscape(segment.FlowID) + "&scenario=" + url.QueryEscape(segment.ScenarioID), Status: scenario.Status, Steps: len(scenario.StepIDs), Unknowns: unknowns})
			if scenario.Status == flowir.Unknown {
				item.Status = flowir.Unknown
			} else if scenario.Status == flowir.Mixed && item.Status == flowir.Observed {
				item.Status = flowir.Mixed
			}
		}
		for i := 1; i < len(journey.Segments); i++ {
			previous, next := journey.Segments[i-1], journey.Segments[i]
			if !workspaceHasJourneyEdge(workspace, previous, next) {
				item.Stale, item.Status = true, flowir.Unknown
			}
		}
		item.Selected = journey.ID == selected
		items = append(items, item)
	}
	if selected == "" {
		for i := range items {
			if !items[i].Stale {
				items[i].Selected, selected = true, items[i].ID
				break
			}
		}
	}
	for i := range items {
		if items[i].ID == selected {
			return journeyNavigation{Journeys: items, Selected: &items[i]}
		}
	}
	return journeyNavigation{Journeys: items}
}

func journeyScenario(workspace workspaceDocument, segment ontology.JourneySegment) (flowir.Document, flowir.Scenario, bool) {
	for _, document := range workspace.Flows {
		if document.Current.ID != segment.FlowID {
			continue
		}
		for _, scenario := range document.Scenarios {
			if scenario.ID == segment.ScenarioID {
				return document, scenario, true
			}
		}
	}
	return flowir.Document{}, flowir.Scenario{}, false
}

func scenarioUnknowns(document flowir.Document, scenario flowir.Scenario) int {
	steps := map[string]bool{}
	for _, id := range scenario.StepIDs {
		steps[id] = true
	}
	unknowns := 0
	for _, unknown := range document.Unknowns {
		for _, id := range unknown.RelatedSteps {
			if steps[id] {
				unknowns++
				break
			}
		}
	}
	return unknowns
}

func workspaceHasEdge(workspace workspaceDocument, from, to string) bool {
	for _, edge := range workspace.Edges {
		if edge.From == from && edge.To == to && edge.Status == flowir.Observed {
			return true
		}
	}
	return false
}

// A journey segment is one independent scenario. Two scenarios rooted at the
// same flow are alternatives unless the source proves a transition between
// distinct flow roots, so they cannot be presented as a sequential journey.
func workspaceHasJourneyEdge(workspace workspaceDocument, previous, next ontology.JourneySegment) bool {
	return previous.FlowID != next.FlowID && workspaceHasEdge(workspace, previous.FlowID, next.FlowID)
}

func businessJourneyTargetExists(workspace workspaceDocument, journey ontology.BusinessJourney) bool {
	for _, segment := range journey.Segments {
		if _, _, ok := journeyScenario(workspace, segment); !ok {
			return false
		}
	}
	for i := 1; i < len(journey.Segments); i++ {
		if !workspaceHasJourneyEdge(workspace, journey.Segments[i-1], journey.Segments[i]) {
			return false
		}
	}
	return true
}
