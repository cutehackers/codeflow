package flowview

import (
	"strings"
)

// Layer classification for the architecture map. Presentation-only:
// derived deterministically at read time from existing FlowSpec fields, so the
// published contract stays unchanged.
//
// Baseline lanes mirror spec v3 canonical order (presentation→…→external→unknown)
// plus legacy aliases (page/ui/application/data/state) for backward compat.
const (
	LayerPresentation = "presentation"
	LayerController   = "controller"
	LayerUsecase      = "usecase"
	LayerDomain       = "domain"
	LayerData         = "data"
	LayerInfra        = "infra"
	LayerExternal     = "external"
	LayerUnknown      = "unknown"
	// Legacy aliases (pre-v3 specs and lane overrides)
	LayerPage        = "page"
	LayerState       = "state"
	LayerRepository  = "repository"
	LayerUI          = "ui"
	LayerApplication = "application"
)

// LayerOrder is the fixed top-to-bottom lane order (v3 canonical + legacy state/page/repository for backward compat).
var LayerOrder = []string{LayerPresentation, LayerController, LayerUsecase, LayerState, LayerDomain, LayerData, LayerInfra, LayerExternal, LayerUnknown}

var layerFallbackLabels = map[string]string{
	LayerPresentation: "Presentation",
	LayerController:   "Controller",
	LayerUsecase:      "UseCase",
	LayerDomain:       "Domain",
	LayerData:         "Data",
	LayerInfra:        "Infra",
	LayerExternal:     "API (External)",
	LayerUnknown:      "Unknown",
	// legacy
	LayerPage:       "Page (Flutter)",
	LayerState:      "상태(State)",
	LayerRepository: "Repository",
}

var canonicalForLegacy = map[string]string{
	LayerPage:        LayerPresentation,
	LayerUI:          LayerPresentation,
	LayerApplication: LayerUsecase,
	LayerRepository:  LayerData,
	"api":            LayerExternal,
	// LayerState and LayerData stay as legacy lanes for backward compat — not remapped to usecase.
}

// InferLayer deterministically classifies one step into its architectural
// layer, and reports which project convention (folder or symbol keyword)
// matched — the map uses it to name lanes in the project's own vocabulary.
// Ordered rules (symbol keywords outrank folder paths because real Flutter
// apps keep controllers under presentation/):
//  1. side effect present                        -> external (API)
//  2. controller                                 -> controller / state
//  3. usecase/service                            -> usecase
//  4. notifier/provider/bloc                     -> state
//  5. page/widget/screen/presentation            -> page
//  6. repository/data                            -> repository
//  7. state mutation step                        -> state
//  8. everything else                            -> usecase (business orchestration)
func InferLayer(repoRelativePath, enclosingSymbolPath string, hasStateDelta, hasSideEffect bool) (string, string) {
	if hasSideEffect {
		return LayerExternal, "external"
	}
	path := strings.ToLower(repoRelativePath)
	sym := strings.ToLower(enclosingSymbolPath)

	if containsAny(sym, "controller") {
		if hasStateDelta {
			return LayerState, "controller"
		}
		return LayerController, "controller"
	}
	if containsAny(sym, "usecase", "service", "interactor") {
		return LayerUsecase, "usecase"
	}
	if containsAny(sym, "notifier", "provider") {
		if hasStateDelta {
			return LayerState, "notifier"
		}
		return LayerState, "notifier"
	}
	if containsAny(sym, "bloc", "cubit") {
		if hasStateDelta {
			return LayerState, "bloc"
		}
		return LayerState, "bloc"
	}
	if containsAny(path, "/presentation/", "/pages/", "/features/") && containsAny(sym, "page", "widget", "screen", "dialog") {
		return LayerPage, "widget"
	}
	if containsAny(path, "/presentation/") {
		return LayerPage, "presentation"
	}
	if containsAny(path, "page", "widget", "screen", "dialog") ||
		containsAny(sym, "page", "widget", "screen", "dialog") {
		return LayerPage, "widget"
	}
	if containsAny(sym, "repository") {
		return LayerRepository, "repository"
	}
	if containsAny(path, "/data/", "/domain/", "datasource", "api_client", "dao", "/infrastructure/", "/repository/") ||
		containsAny(sym, "datasource", "apiclient", "store", "dao") {
		return LayerRepository, "data"
	}
	if hasStateDelta || containsAny(sym, "state") {
		return LayerState, "state"
	}
	return LayerApplication, "application"
}

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if n != "" && strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

var conventionLabels = map[string]string{
	"presentation": "Page (Flutter)",
	"widget":       "Page (Flutter)",
	"page":         "Page (Flutter)",
	"controller":   "Controller",
	"notifier":     "상태(State)",
	"bloc":         "상태(State)",
	"usecase":      "UseCase",
	"application":  "UseCase",
	"state":        "상태(State)",
	"repository":   "Repository",
	"data":         "Repository",
	"external":     "API (External)",
	"api":          "API (External)",
}

// laneLabel picks the most frequent project convention in a layer (first seen
// wins ties, keeping the result deterministic) and names the lane with it.
// The state lane prefixes the convention so it never collides with the
// application lane when both steps live in the same controller.
func laneLabel(layer string, conventions []string) string {
	best := ""
	{
		counts := map[string]int{}
		order := []string{}
		for _, c := range conventions {
			if c == "" {
				continue
			}
			if counts[c] == 0 {
				order = append(order, c)
			}
			counts[c]++
		}
		if len(order) == 0 {
			return layerFallbackLabels[layer]
		}
		best = order[0]
		for _, c := range order {
			if counts[c] > counts[best] {
				best = c
			}
		}
	}
	if layer == LayerState {
		switch best {
		case "controller":
			return "상태 변경(Controller)"
		case "notifier":
			return "상태 변경(Notifier)"
		case "bloc":
			return "상태 변경(Bloc)"
		}
	}
	if label, ok := conventionLabels[best]; ok {
		return label
	}
	return layerFallbackLabels[layer]
}

// applyLayers moved to lanes.go: it now runs the adaptive engine
// (seed-and-propagate classification, confidence, manual overrides) while
// keeping the same read-time decoration contract: per-step "layer" and a
// "lanes" array naming each layer with the project's own vocabulary.
