package analysis

import (
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// symbolSpec describes how a declaration node appears in the outline.
type symbolSpec struct {
	kind      protocol.SymbolKind
	nameField string
	detail    string
}

// LSP has no symbol kinds for states, tests or invariants, so those use the
// closest general-purpose kind.
var symbolSpecs = map[string]symbolSpec{
	"p_type_def":                   {protocol.SymbolKindStruct, "name", "type"},
	"foreign_type_def":             {protocol.SymbolKindStruct, "name", "foreign type"},
	"enum_type_declaration":        {protocol.SymbolKindEnum, "name", "enum"},
	"enum_elem":                    {protocol.SymbolKindEnumMember, "name", ""},
	"numbered_enum_elem":           {protocol.SymbolKindEnumMember, "name", ""},
	"event_declaration":            {protocol.SymbolKindEvent, "name", "event"},
	"event_set_declaration":        {protocol.SymbolKindEvent, "name", "eventset"},
	"interface_declaration":        {protocol.SymbolKindInterface, "name", "interface"},
	"impl_machine_declaration":     {protocol.SymbolKindClass, "name", "machine"},
	"spec_machine_declaration":     {protocol.SymbolKindClass, "name", "spec"},
	"scenario_machine_declaration": {protocol.SymbolKindClass, "name", "scenario"},
	"state_declaration":            {protocol.SymbolKindModule, "name", "state"},
	"p_function_declaration":       {protocol.SymbolKindFunction, "name", "fun"},
	"foreign_function_declaration": {protocol.SymbolKindFunction, "name", "foreign fun"},
	"pure_declaration":             {protocol.SymbolKindFunction, "name", "pure"},
	"named_module_declaration":     {protocol.SymbolKindModule, "name", "module"},
	"safety_test_declaration":      {protocol.SymbolKindNamespace, "test_name", "test"},
	"refinement_test_declaration":  {protocol.SymbolKindNamespace, "test_name", "test"},
	"implementation_declaration":   {protocol.SymbolKindNamespace, "impl_name", "implementation"},
	"invariant_declaration":        {protocol.SymbolKindProperty, "name", "invariant"},
	"invariant_group_declaration":  {protocol.SymbolKindNamespace, "name", ""},
	"proof_block_declaration":      {protocol.SymbolKindNamespace, "name", "proof"},
}

// Symbols returns the document outline.
func Symbols(doc *Document) []protocol.DocumentSymbol {
	return childSymbols(doc, doc.Tree.RootNode())
}

func childSymbols(doc *Document, node *sitter.Node) []protocol.DocumentSymbol {
	symbols := []protocol.DocumentSymbol{}
	for i := uint(0); i < node.NamedChildCount(); i++ {
		symbols = append(symbols, nodeSymbols(doc, node.NamedChild(i))...)
	}
	return symbols
}

// nodeSymbols returns the symbols a node contributes. Nodes that aren't
// declarations are transparent: their descendants' symbols are returned
// instead. That also keeps declarations inside ERROR nodes in the outline.
func nodeSymbols(doc *Document, node *sitter.Node) []protocol.DocumentSymbol {
	switch node.Kind() {
	case "var_declaration", "global_param_declaration":
		return variableSymbols(doc, node)
	case "state_entry_anon", "state_entry_fun":
		return []protocol.DocumentSymbol{handlerSymbol(doc, node, "entry", nil)}
	case "state_exit_anon", "state_exit_fun":
		return []protocol.DocumentSymbol{handlerSymbol(doc, node, "exit", nil)}
	case "on_event_do_action_with_fun", "on_event_do_action_with_anon",
		"on_event_goto_state", "on_event_goto_state_with_anon", "on_event_goto_state_with_fun":
		return onEventSymbols(doc, node)
	}

	spec, ok := symbolSpecs[node.Kind()]
	if !ok {
		return childSymbols(doc, node)
	}

	detail := spec.detail
	if node.Kind() == "invariant_group_declaration" {
		// "Lemma" or "Theorem"
		detail = node.Child(0).Kind()
	}

	symbol := newSymbol(doc, node, node.ChildByFieldName(spec.nameField), spec.kind, detail)
	symbol.Children = childSymbols(doc, node)
	return []protocol.DocumentSymbol{symbol}
}

func variableSymbols(doc *Document, node *sitter.Node) []protocol.DocumentSymbol {
	list := firstChildOfKind(node, "identifier_list")
	if list == nil {
		return nil
	}

	detail := "var"
	if node.Kind() == "global_param_declaration" {
		detail = "param"
	}

	symbols := []protocol.DocumentSymbol{}
	for _, name := range list.ChildrenByFieldName("names", list.Walk()) {
		symbols = append(symbols, newSymbol(doc, node, &name, protocol.SymbolKindVariable, detail))
	}
	return symbols
}

// onEventSymbols names a handler after the events it handles, e.g. "on eRequest".
func onEventSymbols(doc *Document, node *sitter.Node) []protocol.DocumentSymbol {
	events := firstChildOfKind(node, "event_list")
	if events == nil {
		return childSymbols(doc, node)
	}
	name := "on " + strings.Join(strings.Fields(events.Utf8Text(doc.Text)), " ")
	return []protocol.DocumentSymbol{handlerSymbol(doc, node, name, events)}
}

func handlerSymbol(doc *Document, node *sitter.Node, name string, selection *sitter.Node) protocol.DocumentSymbol {
	if selection == nil {
		// The `entry` / `exit` keyword.
		selection = node.Child(0)
	}
	rng := doc.Range(node)
	return protocol.DocumentSymbol{
		Name:           name,
		Kind:           protocol.SymbolKindFunction,
		Range:          rng,
		SelectionRange: doc.Range(selection),
		Children:       childSymbols(doc, node),
	}
}

func newSymbol(doc *Document, node, nameNode *sitter.Node, kind protocol.SymbolKind, detail string) protocol.DocumentSymbol {
	rng := doc.Range(node)
	selection := rng
	name := ""
	if nameNode != nil {
		selection = doc.Range(nameNode)
		name = nameNode.Utf8Text(doc.Text)
	}
	if name == "" {
		// Unnamed proof blocks, or a name tree-sitter inserted as MISSING.
		// Clients reject symbols with empty names.
		name = "<" + detail + ">"
	}

	symbol := protocol.DocumentSymbol{
		Name:           name,
		Kind:           kind,
		Range:          rng,
		SelectionRange: selection,
	}
	if detail != "" {
		symbol.Detail = &detail
	}
	return symbol
}

func firstChildOfKind(node *sitter.Node, kind string) *sitter.Node {
	for i := uint(0); i < node.NamedChildCount(); i++ {
		if child := node.NamedChild(i); child.Kind() == kind {
			return child
		}
	}
	return nil
}
