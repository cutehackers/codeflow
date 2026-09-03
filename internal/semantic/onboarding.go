package semantic

import (
	"errors"
	"fmt"
	"strings"
)

// DomainOverview mirrors schemas/domain-overview.schema.json (VS-09).
type DomainOverview struct {
	SchemaID        string        `json:"schemaId"`
	SchemaVersion   int           `json:"schemaVersion"`
	RepositoryID    string        `json:"repositoryId"`
	ComputedBasisID string        `json:"computedBasisId"`
	GenerationID    string        `json:"generationId"`
	Domains         []DomainInfo  `json:"domains"`
	UnmappedModules []string      `json:"unmappedModules"`
	Summary         DomainSummary `json:"summary"`
}

type DomainInfo struct {
	DomainID                string   `json:"domainId"`
	Name                    string   `json:"name"`
	Description             string   `json:"description"`
	RepresentativeFlowCount int      `json:"representativeFlowCount"`
	EntryPoints             []string `json:"entryPoints"`
}

type DomainSummary struct {
	TotalDomains  int     `json:"totalDomains"`
	TotalFlows    int     `json:"totalFlows"`
	CoverageRatio float64 `json:"coverageRatio"`
}

// RepresentativeFlowCatalog mirrors schemas/representative-flow-catalog.schema.json (VS-09).
type RepresentativeFlowCatalog struct {
	SchemaID        string               `json:"schemaId"`
	SchemaVersion   int                  `json:"schemaVersion"`
	CatalogID       string               `json:"catalogId"`
	DomainID        string               `json:"domainId"`
	ComputedBasisID string               `json:"computedBasisId"`
	GenerationID    string               `json:"generationId"`
	Flows           []RepresentativeFlow `json:"flows"`
}

type RepresentativeFlow struct {
	FlowID          string   `json:"flowId"`
	Title           string   `json:"title"`
	EntrySymbol     string   `json:"entrySymbol"`
	ComplexityScore float64  `json:"complexityScore"`
	KeyMutations    []string `json:"keyMutations"`
	GroundedMapID   string   `json:"groundedMapId"`
}

type CandidateEntry struct {
	CandidateID     string `json:"candidateId"`
	EntrySymbolPath string `json:"entrySymbolPath"`
	Domain          string `json:"domain,omitempty"`
	Title           string `json:"title,omitempty"`
}

type OnboardingOptions struct {
	Level   int    `json:"level"` // 1: System/Domain, 2: Catalog, 3: Traversal
	Domain  string `json:"domain,omitempty"`
	BasisID string `json:"basisId,omitempty"`
	GenID   string `json:"genId,omitempty"`
}

// ExploreDomains generates the top-level domain overview and progressive disclosure structure (VS09-A1..A7).
func ExploreDomains(repoID string, candidates []CandidateEntry, opts OnboardingOptions) (*DomainOverview, error) {
	if strings.TrimSpace(repoID) == "" {
		return nil, errors.New("missing_precondition: repositoryId is required")
	}

	basisID := opts.BasisID
	if basisID == "" {
		basisID = "basis-active"
	}
	genID := opts.GenID
	if genID == "" {
		genID = "gen-active"
	}

	domainMap := make(map[string]*DomainInfo)
	var unmapped []string
	totalFlowCount := 0

	for _, c := range candidates {
		domName := strings.TrimSpace(c.Domain)
		if domName == "" {
			unmapped = append(unmapped, c.EntrySymbolPath)
			continue
		}

		domID := "dom-" + strings.ToLower(domName)
		if existing, ok := domainMap[domName]; ok {
			existing.RepresentativeFlowCount++
			existing.EntryPoints = append(existing.EntryPoints, c.EntrySymbolPath)
		} else {
			desc := fmt.Sprintf("%s 관련 비즈니스 로직 및 핵심 흐름", domName)
			domainMap[domName] = &DomainInfo{
				DomainID:                domID,
				Name:                    domName,
				Description:             desc,
				RepresentativeFlowCount: 1,
				EntryPoints:             []string{c.EntrySymbolPath},
			}
		}
		totalFlowCount++
	}

	var domains []DomainInfo
	for _, d := range domainMap {
		domains = append(domains, *d)
	}

	totalItems := totalFlowCount + len(unmapped)
	coverage := 1.0
	if totalItems > 0 {
		coverage = float64(totalFlowCount) / float64(totalItems)
	}

	return &DomainOverview{
		SchemaID:        "https://codeflow.local/schemas/domain-overview.schema.json",
		SchemaVersion:   1,
		RepositoryID:    repoID,
		ComputedBasisID: basisID,
		GenerationID:    genID,
		Domains:         domains,
		UnmappedModules: unmapped,
		Summary: DomainSummary{
			TotalDomains:  len(domains),
			TotalFlows:    totalFlowCount,
			CoverageRatio: coverage,
		},
	}, nil
}

// GetRepresentativeFlowCatalog extracts curated representative flows for a domain (VS09-A2, A3).
func GetRepresentativeFlowCatalog(domainID, basisID, genID string, candidates []CandidateEntry) (*RepresentativeFlowCatalog, error) {
	if domainID == "" {
		domainID = "default"
	}
	if basisID == "" {
		basisID = "basis-active"
	}
	if genID == "" {
		genID = "gen-active"
	}

	var flows []RepresentativeFlow
	for i, c := range candidates {
		if c.Domain == domainID || strings.EqualFold(c.Domain, domainID) || strings.EqualFold("dom-"+c.Domain, domainID) {
			title := c.Title
			if title == "" {
				title = fmt.Sprintf("%s 처리 흐름", c.EntrySymbolPath)
			}
			flowID := fmt.Sprintf("flow-%s-%d", strings.ToLower(domainID), i+1)
			flows = append(flows, RepresentativeFlow{
				FlowID:          flowID,
				Title:           title,
				EntrySymbol:     c.EntrySymbolPath,
				ComplexityScore: 1.0 + float64(i)*0.5,
				KeyMutations:    []string{fmt.Sprintf("%s.Executed", c.EntrySymbolPath)},
				GroundedMapID:   "map-" + flowID,
			})
		}
	}

	catalogID := "cat-" + strings.ToLower(domainID)
	return &RepresentativeFlowCatalog{
		SchemaID:        "https://codeflow.local/schemas/representative-flow-catalog.schema.json",
		SchemaVersion:   1,
		CatalogID:       catalogID,
		DomainID:        domainID,
		ComputedBasisID: basisID,
		GenerationID:    genID,
		Flows:           flows,
	}, nil
}
