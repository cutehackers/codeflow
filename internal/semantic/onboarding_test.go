package semantic

import (
	"testing"
)

func TestExploreDomains_MissingPreconditions(t *testing.T) {
	_, err := ExploreDomains("", nil, OnboardingOptions{})
	if err == nil {
		t.Fatal("expected error when repositoryId is empty")
	}
	if err.Error() != "missing_precondition: repositoryId is required" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExploreDomains_Level1Overview(t *testing.T) {
	candidates := []CandidateEntry{
		{
			CandidateID:     "cand-1",
			EntrySymbolPath: "OrderController.checkout",
			Domain:          "Order",
			Title:           "체크아웃 흐름",
		},
		{
			CandidateID:     "cand-2",
			EntrySymbolPath: "CatalogController.search",
			Domain:          "Catalog",
			Title:           "상품 검색 흐름",
		},
	}

	overview, err := ExploreDomains("shop-backend", candidates, OnboardingOptions{
		Level: 1,
	})
	if err != nil {
		t.Fatalf("ExploreDomains failed: %v", err)
	}

	// VS09-A1, A2
	if overview.RepositoryID != "shop-backend" {
		t.Errorf("expected repositoryId shop-backend, got %s", overview.RepositoryID)
	}
	if len(overview.Domains) != 2 {
		t.Fatalf("expected 2 domains, got %d", len(overview.Domains))
	}
	if overview.Summary.CoverageRatio <= 0 {
		t.Errorf("expected positive coverageRatio, got %f", overview.Summary.CoverageRatio)
	}
}

func TestGetRepresentativeFlowCatalog_Level2(t *testing.T) {
	candidates := []CandidateEntry{
		{
			CandidateID:     "cand-1",
			EntrySymbolPath: "OrderController.checkout",
			Domain:          "Order",
			Title:           "주문 결제 흐름",
		},
		{
			CandidateID:     "cand-2",
			EntrySymbolPath: "OrderController.cancel",
			Domain:          "Order",
			Title:           "주문 취소 흐름",
		},
	}

	cat, err := GetRepresentativeFlowCatalog("Order", "basis-1", "gen-1", candidates)
	if err != nil {
		t.Fatalf("GetRepresentativeFlowCatalog failed: %v", err)
	}

	// VS09-A2, A3
	if cat.DomainID != "Order" {
		t.Errorf("expected domainId Order, got %s", cat.DomainID)
	}
	if len(cat.Flows) != 2 {
		t.Fatalf("expected 2 representative flows, got %d", len(cat.Flows))
	}
	for _, f := range cat.Flows {
		if f.GroundedMapID == "" {
			t.Errorf("flow %s missing groundedMapId", f.FlowID)
		}
		if f.ComplexityScore < 0 {
			t.Errorf("flow %s invalid complexityScore: %f", f.FlowID, f.ComplexityScore)
		}
	}
}

func TestExploreDomains_UnmappedModulesPreserved(t *testing.T) {
	candidates := []CandidateEntry{
		{
			CandidateID:     "cand-1",
			EntrySymbolPath: "OrderController.checkout",
			Domain:          "Order",
		},
		{
			CandidateID:     "cand-orphan",
			EntrySymbolPath: "OrphanWorker.run",
			Domain:          "", // unmapped
		},
	}

	overview, err := ExploreDomains("shop-backend", candidates, OnboardingOptions{})
	if err != nil {
		t.Fatalf("ExploreDomains failed: %v", err)
	}

	// VS09-A4: unmapped modules preserved
	if len(overview.UnmappedModules) != 1 {
		t.Fatalf("expected 1 unmapped module, got %d", len(overview.UnmappedModules))
	}
	if overview.UnmappedModules[0] != "OrphanWorker.run" {
		t.Errorf("expected OrphanWorker.run, got %s", overview.UnmappedModules[0])
	}
}
