'use strict';
const ts = require('../vendor/typescript/typescript');

function controlHeaderEnd(node, tree) {
  return node.getChildren(tree).find(child => child.kind === ts.SyntaxKind.CloseParenToken)?.end;
}

function doConditionHeaderStart(node, tree) {
  return node.getChildren(tree).find(child => child.kind === ts.SyntaxKind.WhileKeyword)?.getStart(tree) || node.expression.getStart(tree);
}

// Build ranges from the same AST used for execution extraction. Character
// positions are converted to UTF-8 only here, before crossing the adapter API.
function syntaxContextReader(source, tree, body) {
  if (tree.parseDiagnostics.length || !body || !ts.isFunctionLike(body.parent)) return () => null;
  const callable=body.parent;
  const nodes=new Map();
  const key=(start,end)=>`${start}:${end}`;
  function index(node) {
    const start=node.getStart(tree);
    if ((ts.isStatement(node) && !ts.isBlock(node)) || ts.isExpression(node)) {
      nodes.set(key(start,node.end),{node,kind:ts.isStatement(node)?'statement':'expression'});
    }
    // Each declarator is a separately evaluated initialization, including in
    // a declaration list. Preserve its own range rather than the whole list.
    if (ts.isVariableDeclaration(node)) {
      nodes.set(key(start,node.end),{node,kind:'statement'});
    }
    if (ts.isDoStatement(node)) {
      const doStart=doConditionHeaderStart(node,tree),end=controlHeaderEnd(node,tree);
      if(end)nodes.set(key(doStart,end),{node,kind:'control_header'});
    } else if (ts.isIfStatement(node) || ts.isSwitchStatement(node) || ts.isForStatement(node) || ts.isForOfStatement(node) || ts.isForInStatement(node) || ts.isWhileStatement(node)) {
      const end=controlHeaderEnd(node,tree);
      if(end)nodes.set(key(start,end),{node,kind:'control_header'});
    }
    ts.forEachChild(node,index);
  }
  index(body);
  const bytes=position=>Buffer.byteLength(source.slice(0,position),'utf8');
  const byteRange=(start,end)=>[bytes(start),bytes(end)];
  const lineRange=(start,end)=>[tree.getLineAndCharacterOfPosition(start).line+1,tree.getLineAndCharacterOfPosition(end-1).line+1];
  const callStart=callable.getStart(tree),callEnd=callable.end,sigEnd=body.getStart(tree);
  return statement=>{
    const {startOffset:start,endOffset:end}=statement;
    const match=nodes.get(key(start,end));
    if(!match || start<sigEnd || end>callEnd) return null;
    let structuralContext={status:'none'};
    for(let parent=match.node.parent;parent && parent!==callable;parent=parent.parent) {
      // Never attribute a nested callback's selection to the outer callable.
      if(ts.isFunctionLike(parent))return null;
      if(ts.isIfStatement(parent) || ts.isConditionalExpression(parent) || ts.isSwitchStatement(parent) || ts.isIterationStatement(parent,false) || ts.isTryStatement(parent)) {
        const from=parent.getStart(tree),to=parent.end;
        structuralContext={status:'present',nodeKind:'condition',byteRange:byteRange(from,to),lineRange:lineRange(from,to)};
        break;
      }
    }
    // A selected header itself sits inside its complete conditional structure.
    if(match.kind==='control_header') {
      const from=match.node.getStart(tree),to=match.node.end;
      structuralContext={status:'present',nodeKind:'condition',byteRange:byteRange(from,to),lineRange:lineRange(from,to)};
    }
    return {
      statement:{nodeKind:match.kind,byteRange:byteRange(start,end),lineRange:lineRange(start,end)},
      structuralContext,
      callable:{signature:'',signatureByteRange:byteRange(callStart,sigEnd),signatureLineRange:lineRange(callStart,sigEnd),byteRange:byteRange(callStart,callEnd),lineRange:lineRange(callStart,callEnd)},
    };
  };
}
module.exports={syntaxContextReader,controlHeaderEnd,doConditionHeaderStart};
