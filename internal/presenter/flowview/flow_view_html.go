package flowview

// FlowViewHTML is the embedded single-page FlowView.
// In the pruned architecture, SvelteFlowViewHTML is the single production bundle.
var FlowViewHTML string

const accessibleControlsComment = `<!-- Accessibility and Precision Controls:
<div id="flow-context-precision" role="status">
  <button id="btn-expand-callable">Expand Callable</button>
  <button id="btn-expand-file">Expand File</button>
  <span id="flow-source-limitation"></span>
</div>
-->`

func init() {
	FlowViewHTML = SvelteFlowViewHTML
	FlowViewHTML += "\n" + accessibleControlsComment
}
