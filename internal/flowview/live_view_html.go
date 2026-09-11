package flowview

import _ "embed"

// LiveViewTemplate identifies the user-designated template shared by the
// standalone sample and product renderer. Regenerate after editing the sample.
const LiveViewTemplate = "live-semantic-map-prototype.html"

// LiveSemanticTemplate is retained as an alias for LiveViewTemplate for backward compatibility.
const LiveSemanticTemplate = LiveViewTemplate

//go:generate python3 ../../scripts/generate_live_semantic.py

// LiveViewHTML is the archived prototype that defined the live template.
// The /live route now serves the 7-lane FlowViewHTML in live mode; this
// document remains generated from the designated template for reference.

//go:embed live_view.html
var LiveViewHTML string

// LiveSemanticHTML is retained as an alias for LiveViewHTML for backward compatibility.
var LiveSemanticHTML string

func init() {
	LiveSemanticHTML = LiveViewHTML
}
