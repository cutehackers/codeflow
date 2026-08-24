package ontology

import (
	"reflect"
	"testing"
)

func TestBusinessJourneyPersistsOnlyReviewedStableScenarioReferences(t *testing.T) {
	repo := t.TempDir()
	journey := BusinessJourney{ID: "complete-signup", Title: " 가입을 완료합니다 ", Outcome: " 홈으로 이동 ", Segments: []JourneySegment{{FlowID: "route:/join", ScenarioID: "scenario:join"}, {FlowID: "route:/home", ScenarioID: "scenario:home"}}}
	stored, err := SaveBusinessJourney(repo, journey)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "confirmed" || stored.Title != "가입을 완료합니다" || stored.Outcome != "홈으로 이동" {
		t.Fatalf("journey lost its reviewed normalization: %#v", stored)
	}
	loaded, err := LoadBusinessJourneys(repo)
	if err != nil || len(loaded) != 1 || !reflect.DeepEqual(loaded[0], stored) {
		t.Fatalf("journey was not deterministically persisted: %#v %v", loaded, err)
	}
	if _, err := SaveBusinessJourney(repo, BusinessJourney{ID: "Signup", Title: "bad", Segments: []JourneySegment{{FlowID: "route:/join", ScenarioID: "one"}}}); err == nil {
		t.Fatal("unstable journey id was accepted")
	}
}
