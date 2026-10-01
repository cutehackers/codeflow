import type {DisplayedLine, FlowTaskViewData, RequestedFlowBlock} from '../types/flow';

export function blockSourceLines(data: FlowTaskViewData, block: RequestedFlowBlock, expanded: boolean): Array<{line: DisplayedLine; omittedBefore: boolean}> {
  const source = data.sourceFiles?.[block.repoRelativePath] || [];
  const lines = source.filter(line => line.lineNumber >= block.lineRange[0] && line.lineNumber <= block.lineRange[1]);
  const visible = expanded || lines.length <= 24 ? lines : lines.filter((line, index) => index < 3 || index >= lines.length - 3 || block.highlights.some(highlight => line.lineNumber >= highlight.lineRange[0] - 2 && line.lineNumber <= highlight.lineRange[1] + 2));
  return visible.map((line, index) => ({line, omittedBefore: index > 0 && visible[index - 1].lineNumber + 1 < line.lineNumber}));
}

// The presenter supplies UTF-16 columns from the verified snapshot statement.
export function blockLineParts(data: FlowTaskViewData, block: RequestedFlowBlock, line: DisplayedLine): Array<{text: string; stepId?: string}> {
  const selections = block.highlights.flatMap(highlight => {
    return highlight.lineSelections.filter(selection => selection.lineNumber === line.lineNumber).map(selection => ({stepId: highlight.stepId, start: selection.startColumn, end: selection.endColumn, text: line.text.slice(selection.startColumn, selection.endColumn)}));
  }).sort((a, b) => a.start - b.start);
  const parts: Array<{text: string; stepId?: string}> = [];
  let cursor = 0;
  for (const selection of selections) {
    if (selection.start < cursor || line.text.slice(selection.start, selection.end) !== selection.text) continue;
    if (selection.start > cursor) parts.push({text: line.text.slice(cursor, selection.start)});
    parts.push({text: selection.text, stepId: selection.stepId});
    cursor = selection.end;
  }
  if (cursor < line.text.length || !parts.length) parts.push({text: line.text.slice(cursor)});
  return parts;
}

// Overlapping exact spans can hide a nested statement beneath its enclosing
// statement. Keep only these otherwise inaccessible steps in a small chooser.
export function blockDetailAlternatives(data: FlowTaskViewData, block: RequestedFlowBlock): RequestedFlowBlock['highlights'] {
  const represented = new Set(blockSourceLines(data, block, true).flatMap(item => blockLineParts(data, block, item.line).flatMap(part => part.stepId ? [part.stepId] : [])));
  return block.highlights.filter(highlight => !represented.has(highlight.stepId));
}
