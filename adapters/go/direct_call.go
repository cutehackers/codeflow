package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"sort"
	"strconv"
	"strings"
)

type goDirectCall struct {
	callerOrdinal       int
	continuationOrdinal int
	insertedCall        map[string]any
	targetSymbol        string
	unresolvedSymbol    string
	steps               []map[string]any
	edges               []map[string]any
}

// expandDirectGoCalls includes only calls whose concrete declaration and
// straight-line return are both proven by this source file. Other calls keep
// the adapter's explicit unknown boundary.
func expandDirectGoCalls(result map[string]any, path, source, snapshotID string, valueRoles ...bool) map[string]any {
	baseSteps, stepsOK := result["steps"].([]map[string]any)
	baseEdges, edgesOK := result["edges"].([]map[string]any)
	entry, entryOK := result["entrySymbolPath"].(string)
	if !stepsOK || !edgesOK || !entryOK || len(baseSteps) == 0 {
		return result
	}
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, source, 0)
	if err != nil {
		return result
	}
	rootSymbol := strings.TrimPrefix(entry, path+"#")
	var root *ast.FuncDecl
	declarations := make(map[string]*ast.FuncDecl)
	duplicates := make(map[string]bool)
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		symbol := goFunctionSymbol(fn)
		if symbol == rootSymbol {
			root = fn
		}
		if duplicates[symbol] {
			continue
		}
		if declarations[symbol] == nil {
			declarations[symbol] = fn
		} else {
			delete(declarations, symbol)
			duplicates[symbol] = true
		}
	}
	if root == nil {
		return result
	}
	stepByRange := make(map[[2]int]int, len(baseSteps))
	for _, step := range baseSteps {
		anchor, ok := step["anchor"].(map[string]any)
		if !ok {
			continue
		}
		span, ok := anchor["byteRange"].([]int)
		ordinal, ordinalOK := step["ordinal"].(int)
		if ok && ordinalOK && len(span) == 2 {
			stepByRange[[2]int{span[0], span[1]}] = ordinal
		}
	}
	candidateCalls := make(map[int]goDirectCall)
	ast.Inspect(root.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		var statement ast.Stmt
		var call *ast.CallExpr
		insertBefore := false
		switch current := node.(type) {
		case *ast.ExprStmt:
			statement = current
			call, _ = current.X.(*ast.CallExpr)
		case *ast.AssignStmt:
			if len(current.Rhs) == 1 {
				statement = current
				call, _ = current.Rhs[0].(*ast.CallExpr)
				insertBefore = true
			}
		case *ast.ReturnStmt:
			if len(current.Results) == 1 {
				statement = current
				call, _ = current.Results[0].(*ast.CallExpr)
				insertBefore = true
			}
		}
		if statement == nil || call == nil || goHasNestedCall(call.Args) {
			return true
		}
		span := [2]int{fileSet.Position(statement.Pos()).Offset, fileSet.Position(statement.End()).Offset}
		callerOrdinal := stepByRange[span]
		if callerOrdinal == 0 {
			return true
		}
		targetSymbol := goDirectCallTarget(root, call, declarations)
		if targetSymbol == "" || targetSymbol == rootSymbol {
			return true
		}
		continuation := callerOrdinal
		if !insertBefore {
			continuation = goDirectCallContinuation(baseEdges, callerOrdinal)
		}
		if continuation == 0 {
			return true
		}
		calleeEntry := path + "#" + targetSymbol
		callee, err := sliceGoAST(result["candidateId"].(string), calleeEntry, path, source, snapshotID, valueRoles...)
		if err != nil {
			return true
		}
		calleeSteps, stepsOK := callee["steps"].([]map[string]any)
		calleeEdges, edgesOK := callee["edges"].([]map[string]any)
		if !stepsOK || !edgesOK || !goHasProvenLinearReturn(calleeSteps, calleeEdges) {
			return true
		}
		funStart := fileSet.Position(call.Fun.Pos()).Offset
		funEnd := fileSet.Position(call.Fun.End()).Offset
		candidate := goDirectCall{
			callerOrdinal: callerOrdinal, continuationOrdinal: continuation,
			targetSymbol: targetSymbol, unresolvedSymbol: path + "#" + source[funStart:funEnd],
			steps: calleeSteps, edges: calleeEdges,
		}
		if insertBefore {
			candidate.insertedCall = goDirectCallExpressionStep(baseSteps[callerOrdinal-1], call, fileSet, source)
		}
		candidateCalls[callerOrdinal] = candidate
		return true
	})
	if len(candidateCalls) == 0 {
		return result
	}
	newSteps := make([]map[string]any, 0, len(baseSteps)+len(candidateCalls)*2)
	oldOrdinals := make(map[int]int, len(baseSteps))
	entryOrdinals := make(map[int]int, len(baseSteps))
	callOrdinals := make(map[int]int, len(candidateCalls))
	calleeOrdinals := make(map[int]map[int]int, len(candidateCalls))
	for _, original := range baseSteps {
		oldOrdinal := original["ordinal"].(int)
		call, expands := candidateCalls[oldOrdinal]
		if expands && call.insertedCall != nil {
			callOrdinal := len(newSteps) + 1
			inserted := maps.Clone(call.insertedCall)
			inserted["ordinal"] = callOrdinal
			newSteps = append(newSteps, inserted)
			entryOrdinals[oldOrdinal] = callOrdinal
			callOrdinals[oldOrdinal] = callOrdinal
		} else {
			entryOrdinals[oldOrdinal] = len(newSteps) + 1
		}
		if expands {
			if call.insertedCall == nil {
				callOrdinals[oldOrdinal] = entryOrdinals[oldOrdinal]
			}
			calleeOrdinals[oldOrdinal] = make(map[int]int, len(call.steps))
			invocationID := digest([]byte(entry + ":" + strconv.Itoa(callOrdinals[oldOrdinal]) + ":" + call.targetSymbol))
			if call.insertedCall == nil {
				step := maps.Clone(original)
				step["ordinal"] = len(newSteps) + 1
				oldOrdinals[oldOrdinal] = len(newSteps) + 1
				newSteps = append(newSteps, step)
			}
			for _, originalCallee := range call.steps {
				calleeOrdinal := originalCallee["ordinal"].(int)
				mappedOrdinal := len(newSteps) + 1
				calleeOrdinals[oldOrdinal][calleeOrdinal] = mappedOrdinal
				calleeStep := maps.Clone(originalCallee)
				calleeStep["ordinal"] = mappedOrdinal
				calleeStep["invocationId"] = invocationID
				calleeStep["callerStepOrdinal"] = callOrdinals[oldOrdinal]
				newSteps = append(newSteps, calleeStep)
			}
			if call.insertedCall != nil {
				oldOrdinals[oldOrdinal] = len(newSteps) + 1
				step := maps.Clone(original)
				step["ordinal"] = len(newSteps) + 1
				newSteps = append(newSteps, step)
			}
			continue
		}
		oldOrdinals[oldOrdinal] = len(newSteps) + 1
		step := maps.Clone(original)
		step["ordinal"] = len(newSteps) + 1
		newSteps = append(newSteps, step)
	}
	newEdges := make([]map[string]any, 0, len(baseEdges)+len(candidateCalls)*4)
	for _, original := range baseEdges {
		if original["kind"] == "unknown_edge" {
			if call, ok := candidateCalls[original["stepOrdinal"].(int)]; ok && original["toSymbolPath"] == call.unresolvedSymbol {
				continue
			}
		}
		newEdges = append(newEdges, remapGoDirectCallEdge(original, oldOrdinals, entryOrdinals))
	}
	orderedCalls := make([]int, 0, len(candidateCalls))
	for ordinal := range candidateCalls {
		orderedCalls = append(orderedCalls, ordinal)
	}
	sort.Ints(orderedCalls)
	for _, originalOrdinal := range orderedCalls {
		call := candidateCalls[originalOrdinal]
		mapped := calleeOrdinals[call.callerOrdinal]
		for _, edge := range call.edges {
			newEdges = append(newEdges, remapGoDirectCallEdge(edge, mapped, mapped))
		}
		callerOrdinal := callOrdinals[call.callerOrdinal]
		calleeEntryOrdinal := mapped[call.steps[0]["ordinal"].(int)]
		newEdges = append(newEdges, map[string]any{
			"kind": "resolved_cross_file", "toSymbolPath": path + "#" + call.targetSymbol,
			"resolutionStatus": "resolved", "depth": 1,
			"stepOrdinal": callerOrdinal, "targetStepOrdinal": calleeEntryOrdinal,
		})
		calleeReturnOrdinal := mapped[call.steps[len(call.steps)-1]["ordinal"].(int)]
		newEdges = append(newEdges, map[string]any{
			"kind": "return", "toSymbolPath": entry,
			"resolutionStatus": "resolved", "depth": 1,
			"stepOrdinal": calleeReturnOrdinal, "targetStepOrdinal": oldOrdinals[call.continuationOrdinal],
		})
		if call.insertedCall != nil {
			newEdges = append(newEdges, map[string]any{
				"kind": "control_flow", "toSymbolPath": entry,
				"resolutionStatus": "resolved", "depth": 0,
				"stepOrdinal": callerOrdinal, "targetStepOrdinal": oldOrdinals[call.callerOrdinal],
			})
		}
	}
	result["steps"] = newSteps
	result["edges"] = newEdges
	return result
}

func goDirectCallExpressionStep(original map[string]any, call *ast.CallExpr, fileSet *token.FileSet, source string) map[string]any {
	start := fileSet.Position(call.Pos())
	end := fileSet.Position(call.End())
	span := source[start.Offset:end.Offset]
	step := maps.Clone(original)
	step["kind"] = "call"
	delete(step, "valueRole")
	step["description"] = span + " 호출"
	anchor := maps.Clone(original["anchor"].(map[string]any))
	anchor["byteRange"] = []int{start.Offset, end.Offset}
	anchor["spanHash"] = digest([]byte(span))
	anchor["canonicalAstFingerprint"] = digest([]byte(span))
	step["anchor"] = anchor
	context := maps.Clone(original["flowContext"].(map[string]any))
	context["statement"] = map[string]any{
		"nodeKind": "expression", "byteRange": []int{start.Offset, end.Offset},
		"lineRange": []int{start.Line, end.Line},
	}
	step["flowContext"] = context
	return step
}

func goFunctionSymbol(fn *ast.FuncDecl) string {
	if receiver := receiverTypeName(fn.Recv); receiver != "" {
		return receiver + "." + fn.Name.Name
	}
	return fn.Name.Name
}

func goDirectCallTarget(root *ast.FuncDecl, call *ast.CallExpr, declarations map[string]*ast.FuncDecl) string {
	name := ""
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if goNameShadowed(root, fun.Name) {
			return ""
		}
		name = fun.Name
	case *ast.SelectorExpr:
		receiver, ok := fun.X.(*ast.Ident)
		if !ok || root.Recv == nil || len(root.Recv.List) != 1 || len(root.Recv.List[0].Names) != 1 ||
			receiver.Name != root.Recv.List[0].Names[0].Name {
			return ""
		}
		name = receiverTypeName(root.Recv) + "." + fun.Sel.Name
	}
	if declarations[name] == nil {
		return ""
	}
	return name
}

func goNameShadowed(root *ast.FuncDecl, name string) bool {
	for _, fields := range []*ast.FieldList{root.Recv, root.Type.Params, root.Type.Results} {
		if fields == nil {
			continue
		}
		for _, field := range fields.List {
			for _, identifier := range field.Names {
				if identifier.Name == name {
					return true
				}
			}
		}
	}
	shadowed := false
	ast.Inspect(root.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		switch declaration := node.(type) {
		case *ast.AssignStmt:
			if declaration.Tok == token.DEFINE {
				for _, left := range declaration.Lhs {
					if identifier, ok := left.(*ast.Ident); ok && identifier.Name == name {
						shadowed = true
					}
				}
			}
		case *ast.ValueSpec:
			for _, identifier := range declaration.Names {
				if identifier.Name == name {
					shadowed = true
				}
			}
		case *ast.RangeStmt:
			if declaration.Tok == token.DEFINE {
				for _, value := range []ast.Expr{declaration.Key, declaration.Value} {
					if identifier, ok := value.(*ast.Ident); ok && identifier.Name == name {
						shadowed = true
					}
				}
			}
		}
		return !shadowed
	})
	return shadowed
}

func goHasNestedCall(expressions []ast.Expr) bool {
	for _, expression := range expressions {
		found := false
		ast.Inspect(expression, func(node ast.Node) bool {
			if _, ok := node.(*ast.CallExpr); ok {
				found = true
				return false
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

func goDirectCallContinuation(edges []map[string]any, callerOrdinal int) int {
	continuation := 0
	for _, edge := range edges {
		if edge["stepOrdinal"] != callerOrdinal || edge["kind"] != "control_flow" || edge["resolutionStatus"] != "resolved" {
			continue
		}
		target, ok := edge["targetStepOrdinal"].(int)
		if !ok || target <= callerOrdinal || edge["conditions"] != nil || continuation != 0 {
			return 0
		}
		continuation = target
	}
	return continuation
}

func goHasProvenLinearReturn(steps, edges []map[string]any) bool {
	if len(steps) == 0 || steps[len(steps)-1]["kind"] != "return" || len(edges) != len(steps)-1 {
		return false
	}
	for i, step := range steps {
		if i < len(steps)-1 && step["kind"] != "mutation" && step["kind"] != "call" {
			return false
		}
	}
	for i, edge := range edges {
		if edge["kind"] != "control_flow" || edge["resolutionStatus"] != "resolved" ||
			edge["stepOrdinal"] != i+1 || edge["targetStepOrdinal"] != i+2 || edge["conditions"] != nil {
			return false
		}
	}
	return true
}

func remapGoDirectCallEdge(original map[string]any, sourceOrdinals, targetOrdinals map[int]int) map[string]any {
	edge := maps.Clone(original)
	if ordinal, ok := edge["stepOrdinal"].(int); ok {
		edge["stepOrdinal"] = sourceOrdinals[ordinal]
	}
	if ordinal, ok := edge["targetStepOrdinal"].(int); ok {
		edge["targetStepOrdinal"] = targetOrdinals[ordinal]
	}
	if conditions, ok := edge["conditions"].([]map[string]any); ok {
		mapped := make([]map[string]any, len(conditions))
		for i, condition := range conditions {
			mapped[i] = maps.Clone(condition)
			if ordinal, ok := mapped[i]["stepOrdinal"].(int); ok {
				mapped[i]["stepOrdinal"] = sourceOrdinals[ordinal]
			}
		}
		edge["conditions"] = mapped
	}
	return edge
}
