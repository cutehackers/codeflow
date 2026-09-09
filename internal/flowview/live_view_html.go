package flowview

import _ "embed"

// LiveViewTemplate identifies the user-designated template shared by the
// standalone sample and product renderer. Regenerate after editing the sample.
const LiveViewTemplate = "live-semantic-map-prototype.html"

// LiveSemanticTemplate is retained as an alias for LiveViewTemplate for backward compatibility.
const LiveSemanticTemplate = LiveViewTemplate

//go:generate python3 ../../scripts/generate_live_semantic.py

// LiveViewHTML (or Live Semantic View) is a separate code-comprehension surface.
// FlowViewHTML remains FlowView; only explicit live entry points serve this document.
//
//go:embed live_view.html
var LiveViewHTML string

// LiveSemanticHTML is retained as an alias for LiveViewHTML for backward compatibility.
var LiveSemanticHTML string

func init() {
	LiveSemanticHTML = LiveViewHTML
}
