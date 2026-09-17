package flowview

import _ "embed"

// SvelteFlowViewHTML is the compiled Svelte 5 single-page application bundle
// providing the modular FlowView with Macro Context FlowSequence and Blast Radius Radar.
//
//go:embed svelte_flow_view.html
var SvelteFlowViewHTML string
