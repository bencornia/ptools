package analysis

import (
	protocol "github.com/tliron/glsp/protocol_3_16"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// P keeps state and module names apart from everything else: `goto Init` can
// only mean a state, and `module Client = { Client };` declares a module named
// after the machine it contains.
type namespace int

const (
	nsValue namespace = iota
	nsState
	nsModule
)

// declaration is a declared name: the identifier that introduces it, and the
// document containing it.
type declaration struct {
	doc  *Document
	node *sitter.Node
	// name is how references spell it, which differs from the node's text
	// for invariants inside a Lemma or Theorem.
	name string
}

// Definition returns where the identifier at pos in doc is declared. It
// searches the enclosing function and machine first, then the top-level
// declarations of every document in workspace (which should include doc).
func Definition(workspace []*Document, doc *Document, pos protocol.Position) []protocol.Location {
	ident := identifierAt(doc, pos)
	if ident == nil {
		return nil
	}
	locs := []protocol.Location{}
	for _, d := range resolve(workspace, doc, ident) {
		locs = append(locs, protocol.Location{URI: d.doc.URI, Range: d.doc.Range(d.node)})
	}
	return locs
}

func resolve(workspace []*Document, doc *Document, ident *sitter.Node) []declaration {
	if isFieldName(ident) {
		return nil
	}
	if isGroupedInvariantName(ident) {
		// References spell it <group>_<name>, so it can't be looked up by
		// its own text.
		return declarationsOf(doc, []*sitter.Node{ident})
	}
	name := ident.Utf8Text(doc.Text)
	ns := namespaceOf(ident)

	for scope := ident.Parent(); scope != nil; scope = scope.Parent() {
		if found := named(declarationsOf(doc, scopeDeclarations(scope, ns)), name); len(found) > 0 {
			return found
		}
	}
	if ns == nsState {
		return nil
	}

	found := named(globalDeclarationsIn(workspace, ns), name)
	if len(found) == 0 && isTestedKind(ident) {
		// `x is S` also tests which state machine x is in.
		found = named(testedStates(workspace, doc, ident.Parent()), name)
	}
	return found
}

func isGroupedInvariantName(ident *sitter.Node) bool {
	inv := ident.Parent()
	return inv.Kind() == "invariant_declaration" && sameNode(inv.ChildByFieldName("name"), ident) &&
		inv.Parent() != nil && inv.Parent().Kind() == "invariant_group_declaration"
}

func isTestedKind(ident *sitter.Node) bool {
	parent := ident.Parent()
	return parent.Kind() == "test_expression" && sameNode(parent.ChildByFieldName("kind"), ident)
}

// testedStates returns the states that `instance is S` can name. Like the
// compiler, it uses the states of the instance's machine type when that's
// known, and every machine's states otherwise.
func testedStates(workspace []*Document, doc *Document, test *sitter.Node) []declaration {
	machines := []declaration{}
	for _, d := range globalDeclarationsIn(workspace, nsValue) {
		if machineBody(d.node.Parent()) != nil {
			machines = append(machines, d)
		}
	}

	if instance := test.ChildByFieldName("instance"); instance != nil && instance.Kind() == "identifier" {
		if found := resolve(workspace, doc, instance); len(found) == 1 {
			if typed := named(machines, declaredTypeName(found[0])); len(typed) > 0 {
				machines = typed
			}
		}
	}

	states := []declaration{}
	for _, m := range machines {
		body := machineBody(m.node.Parent())
		states = append(states, declarationsOf(m.doc, machineMembers(body, nsState))...)
	}
	return states
}

// declaredTypeName returns the type name a variable or parameter is declared
// with, as in `var c: Coordinator` or `(c: Coordinator)`, or "" if it isn't
// declared with a named type.
func declaredTypeName(d declaration) string {
	decl := d.node.Parent()
	if decl.Kind() == "identifier_list" {
		decl = decl.Parent()
	}
	if decl.Kind() != "function_param" && decl.Kind() != "var_declaration" {
		return ""
	}
	if typ := firstChildOfKind(decl, "named_type"); typ != nil {
		if name := typ.ChildByFieldName("name"); name != nil {
			return name.Utf8Text(d.doc.Text)
		}
	}
	return ""
}

// identifierAt returns the identifier containing pos, or the one ending at
// pos, since editors often place the cursor just after a word.
func identifierAt(doc *Document, pos protocol.Position) *sitter.Node {
	root := doc.Tree.RootNode()
	point := doc.Point(pos)
	if n := root.NamedDescendantForPointRange(point, point); n != nil && n.Kind() == "identifier" {
		return n
	}
	if point.Column > 0 {
		point.Column--
		if n := root.NamedDescendantForPointRange(point, point); n != nil && n.Kind() == "identifier" {
			return n
		}
	}
	return nil
}

// isFieldName reports whether ident names a tuple field, which can't be
// resolved without knowing the tuple's type.
func isFieldName(ident *sitter.Node) bool {
	parent := ident.Parent()
	switch parent.Kind() {
	case "named_tuple_access_expression":
		return sameNode(parent.ChildByFieldName("field"), ident)
	case "named_tuple_lvalue":
		// lvalue '.' identifier
		return sameNode(parent.NamedChild(1), ident)
	case "named_tuple_body", "identifier_type":
		return true
	}
	return false
}

func namespaceOf(ident *sitter.Node) namespace {
	parent := ident.Parent()
	switch parent.Kind() {
	case "state_name":
		return nsState
	case "state_declaration":
		if sameNode(parent.ChildByFieldName("name"), ident) {
			return nsState
		}
	case "named_module", "named_module_declaration":
		// A named_module is a module referenced in a module expression, as in
		// `union Client, Bank`. Machines in `{ Client }` are bind_expressions.
		return nsModule
	}
	return nsValue
}

// scopeDeclarations returns the name nodes declared directly in scope.
func scopeDeclarations(scope *sitter.Node, ns namespace) []*sitter.Node {
	switch ns {
	case nsModule:
		// Modules are only declared at the top level.
		return nil
	case nsState:
		if body := machineBody(scope); body != nil {
			return machineMembers(body, ns)
		}
		return nil
	}

	switch scope.Kind() {
	case "p_function_declaration", "foreign_function_declaration", "pure_declaration":
		names := parameters(scope)
		if body := firstChildOfKind(scope, "function_body"); body != nil {
			names = append(names, localVariables(body)...)
		}
		return names
	case "anon_event_handler", "no_param_anon_event_handler":
		// The optional `(payload: T)` parameter, then the body's locals.
		names := []*sitter.Node{}
		if param := firstChildOfKind(scope, "function_param"); param != nil {
			names = append(names, param.ChildByFieldName("name"))
		}
		if body := firstChildOfKind(scope, "function_body"); body != nil {
			names = append(names, localVariables(body)...)
		}
		return names
	case "quant_expression":
		if bound := scope.ChildByFieldName("bound"); bound != nil {
			return paramNames(bound)
		}
	}

	if body := machineBody(scope); body != nil {
		return machineMembers(body, ns)
	}
	return nil
}

func machineBody(scope *sitter.Node) *sitter.Node {
	switch scope.Kind() {
	case "impl_machine_declaration", "spec_machine_declaration", "scenario_machine_declaration":
		return firstChildOfKind(scope, "machine_body")
	}
	return nil
}

// machineMembers returns a machine's states, or its variables and functions.
func machineMembers(body *sitter.Node, ns namespace) []*sitter.Node {
	names := []*sitter.Node{}
	for _, entry := range namedChildren(body) {
		if entry.Kind() != "machine_entry" || entry.NamedChildCount() == 0 {
			continue
		}
		decl := entry.NamedChild(0)
		switch {
		case ns == nsState && decl.Kind() == "state_declaration":
			names = appendName(names, decl, "name")
		case ns == nsValue && decl.Kind() == "var_declaration":
			names = append(names, identifierListNames(decl)...)
		case ns == nsValue && decl.Kind() == "function_declaration" && decl.NamedChildCount() > 0:
			names = appendName(names, decl.NamedChild(0), "name")
		}
	}
	return names
}

// globalNameFields maps top-level declaration kinds to their name field.
var globalNameFields = map[string]string{
	"p_type_def":                   "name",
	"foreign_type_def":             "name",
	"event_declaration":            "name",
	"event_set_declaration":        "name",
	"interface_declaration":        "name",
	"impl_machine_declaration":     "name",
	"spec_machine_declaration":     "name",
	"scenario_machine_declaration": "name",
	"p_function_declaration":       "name",
	"foreign_function_declaration": "name",
	"pure_declaration":             "name",
	"safety_test_declaration":      "test_name",
	"refinement_test_declaration":  "test_name",
	"implementation_declaration":   "impl_name",
	"invariant_declaration":        "name",
	"proof_block_declaration":      "name",
}

func globalDeclarationsIn(workspace []*Document, ns namespace) []declaration {
	decls := []declaration{}
	for _, doc := range workspace {
		decls = append(decls, globalDeclarations(doc, doc.Tree.RootNode(), ns)...)
	}
	return decls
}

// globalDeclarations returns the top-level declarations under node in
// namespace ns.
func globalDeclarations(doc *Document, node *sitter.Node, ns namespace) []declaration {
	decls := []declaration{}
	for _, child := range namedChildren(node) {
		switch child.Kind() {
		case "type_def_declaration", "function_declaration", "test_declaration", "ERROR":
			// Wrappers around a single declaration, or tree-sitter's error
			// recovery, which can still hold whole declarations.
			decls = append(decls, globalDeclarations(doc, child, ns)...)
			continue
		case "named_module_declaration":
			if ns == nsModule {
				decls = append(decls, declarationsOf(doc, appendName(nil, child, "name"))...)
			}
			continue
		}

		if ns != nsValue {
			continue
		}
		var names []*sitter.Node
		switch child.Kind() {
		case "enum_type_declaration":
			names = appendName(names, child, "name")
			for _, list := range namedChildren(child) {
				for _, elem := range namedChildren(list) {
					names = appendName(names, elem, "name")
				}
			}
		case "invariant_group_declaration":
			decls = append(decls, invariantGroupDeclarations(doc, child)...)
		case "global_param_declaration":
			names = identifierListNames(child)
		default:
			if field, ok := globalNameFields[child.Kind()]; ok {
				names = appendName(names, child, field)
			}
		}
		decls = append(decls, declarationsOf(doc, names)...)
	}
	return decls
}

// invariantGroupDeclarations returns a Lemma or Theorem and its invariants,
// which the compiler names <group>_<invariant>.
func invariantGroupDeclarations(doc *Document, group *sitter.Node) []declaration {
	groupName := group.ChildByFieldName("name")
	if groupName == nil {
		return nil
	}
	decls := declarationsOf(doc, []*sitter.Node{groupName})
	prefix := groupName.Utf8Text(doc.Text) + "_"
	for _, inv := range namedChildren(group) {
		if name := inv.ChildByFieldName("name"); inv.Kind() == "invariant_declaration" && name != nil {
			decls = append(decls, declaration{doc: doc, node: name, name: prefix + name.Utf8Text(doc.Text)})
		}
	}
	return decls
}

func parameters(fn *sitter.Node) []*sitter.Node {
	names := []*sitter.Node{}
	for _, child := range namedChildren(fn) {
		switch child.Kind() {
		case "function_param_list":
			names = append(names, paramNames(child)...)
		case "function_param":
			// A foreign function's `return (x: T)`.
			names = appendName(names, child, "name")
		}
	}
	return names
}

func paramNames(list *sitter.Node) []*sitter.Node {
	names := []*sitter.Node{}
	for _, param := range namedChildren(list) {
		names = appendName(names, param, "name")
	}
	return names
}

// localVariables returns the variables declared at the top of a function body,
// the only place P allows them.
func localVariables(body *sitter.Node) []*sitter.Node {
	names := []*sitter.Node{}
	for _, child := range namedChildren(body) {
		if child.Kind() == "var_declaration" {
			names = append(names, identifierListNames(child)...)
		}
	}
	return names
}

func identifierListNames(decl *sitter.Node) []*sitter.Node {
	list := firstChildOfKind(decl, "identifier_list")
	if list == nil {
		return nil
	}
	names := []*sitter.Node{}
	for _, name := range list.ChildrenByFieldName("names", list.Walk()) {
		names = append(names, &name)
	}
	return names
}

func appendName(names []*sitter.Node, decl *sitter.Node, field string) []*sitter.Node {
	if name := decl.ChildByFieldName(field); name != nil {
		return append(names, name)
	}
	return names
}

// declarationsOf makes declarations of name nodes in doc, named by their text.
func declarationsOf(doc *Document, names []*sitter.Node) []declaration {
	decls := make([]declaration, 0, len(names))
	for _, n := range names {
		decls = append(decls, declaration{doc: doc, node: n, name: n.Utf8Text(doc.Text)})
	}
	return decls
}

func named(decls []declaration, name string) []declaration {
	found := []declaration{}
	for _, d := range decls {
		if d.name == name {
			found = append(found, d)
		}
	}
	return found
}

func namedChildren(node *sitter.Node) []*sitter.Node {
	children := make([]*sitter.Node, 0, node.NamedChildCount())
	for i := uint(0); i < node.NamedChildCount(); i++ {
		children = append(children, node.NamedChild(i))
	}
	return children
}

func sameNode(a, b *sitter.Node) bool {
	return a != nil && b != nil && a.Id() == b.Id()
}
