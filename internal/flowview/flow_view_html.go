package flowview

import _ "embed"

// FlowViewHTML is the embedded single-page FlowView — flow-first review surface:
// business flow explanations, 7-lane architecture map, execution timeline,
// symbol-scoped code evidence, causal impact and honest unknowns.
//
//go:embed flow_view.html
var FlowViewHTML string

// IndexHTML is retained as an alias for FlowViewHTML for backward compatibility.
var IndexHTML string

func init() {
	IndexHTML = FlowViewHTML
}
