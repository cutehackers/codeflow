package flowview

import (
	"strings"
)

// Layer classification for the architecture map. Presentation-only:
// derived deterministically at read time from existing FlowSpec fields, so the
// published contract stays unchanged.
const (
	LayerUI          = "ui"
	LayerApplication = "application"
	LayerState       = "state"
	LayerData        = "data"
	LayerExternal    = "external"
)

// LayerOrder is the fixed top-to-bottom lane order of the architecture map.
var LayerOrder = []string{LayerUI, LayerApplication, LayerState, LayerData, LayerExternal}

var layerFallbackLabels = map[string]string{
	LayerUI:          "화면(UI)",
	LayerApplication: "흐름 제어(Application)",
	LayerState:       "상태(State)",
	LayerData:        "데이터(Data)",
	LayerExternal:    "외부 연동(External)",
}

// InferLayer deterministically classifies one step into its architectural
// layer, and reports which project convention (folder or symbol keyword)
// matched — the map uses it to name lanes in the project's own vocabulary.
// Ordered rules (symbol keywords outrank folder paths because real Flutter
// apps keep controllers under presentation/):
//  1. side effect present                        -> external
//  2. controller/notifier/provider symbol        -> state (mutation) else application
//  3. presentation path/symbol                   -> ui
//  4. data path/symbol                           -> data
//  5. state mutation step                        -> state
//  6. everything else                            -> application
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
		return LayerApplication, "controller"
	}
	if containsAny(sym, "notifier", "provider") {
		if hasStateDelta {
			return LayerState, "notifier"
		}
		return LayerApplication, "notifier"
	}
	if containsAny(sym, "bloc", "cubit") {
		if hasStateDelta {
			return LayerState, "bloc"
		}
		return LayerApplication, "bloc"
	}
	if containsAny(sym, "usecase", "service") {
		return LayerApplication, "usecase"
	}
	if containsAny(path, "/presentation/") {
		return LayerUI, "presentation"
	}
	if containsAny(path, "page", "widget", "screen", "dialog") ||
		containsAny(sym, "page", "widget", "screen", "dialog") {
		return LayerUI, "widget"
	}
	if containsAny(sym, "repository") {
		return LayerData, "repository"
	}
	if containsAny(path, "/data/", "datasource", "api_client", "dao", "/infrastructure/") ||
		containsAny(sym, "datasource", "apiclient", "store", "dao") {
		return LayerData, "data"
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
	"presentation": "화면(Presentation)",
	"widget":       "화면(UI)",
	"controller":   "컨트롤러(Controller)",
	"notifier":     "프로바이더(Notifier)",
	"bloc":         "블록(Bloc)",
	"usecase":      "유스케이스(Usecase)",
	"application":  "흐름 제어(Application)",
	"state":        "상태(State)",
	"repository":   "리포지토리(Repository)",
	"data":         "데이터(Data)",
	"external":     "외부 연동(External)",
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
