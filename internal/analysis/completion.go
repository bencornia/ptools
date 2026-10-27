package analysis

import (
	"slices"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// placeholder is the name Completion types at the cursor. Half-typed code
// such as `x.` or `goto ` doesn't parse, so tree-sitter can't say what goes
// there; with a name in place, it parses as `x.placeholder` or
// `goto placeholder`.
const placeholder = "pLspCompletion"

// completionKinds maps declaration kinds to how completion items show them.
var completionKinds = map[protocol.SymbolKind]protocol.CompletionItemKind{
	protocol.SymbolKindStruct:     protocol.CompletionItemKindStruct,
	protocol.SymbolKindEnum:       protocol.CompletionItemKindEnum,
	protocol.SymbolKindEnumMember: protocol.CompletionItemKindEnumMember,
	protocol.SymbolKindEvent:      protocol.CompletionItemKindEvent,
	protocol.SymbolKindInterface:  protocol.CompletionItemKindInterface,
	protocol.SymbolKindClass:      protocol.CompletionItemKindClass,
	protocol.SymbolKindModule:     protocol.CompletionItemKindModule,
	protocol.SymbolKindFunction:   protocol.CompletionItemKindFunction,
	protocol.SymbolKindNamespace:  protocol.CompletionItemKindModule,
	protocol.SymbolKindProperty:   protocol.CompletionItemKindProperty,
}

// Completion returns the names that can be written at pos in doc: a value's
// fields after a `.`, the machine's states after `goto`, and otherwise the
// names in scope and P's keywords. Clients filter them by what's typed.
func Completion(parser *sitter.Parser, workspace []*Document, doc *Document, pos protocol.Position) []protocol.CompletionItem {
	offset := int(doc.offset(pos))
	text := string(doc.Text[:offset]) + placeholder + string(doc.Text[offset:])
	edited := Parse(parser, doc.URI, doc.Version, text)
	defer edited.Close()

	start, end := uint(offset), uint(offset+len(placeholder))
	ident := edited.Tree.RootNode().NamedDescendantForByteRange(start, end)
	if ident == nil || ident.Kind() != "identifier" {
		// In a comment or string, say.
		return nil
	}
	// Look up names in the edited text, which is the one ident is in.
	workspace = slices.Clone(workspace)
	for i, d := range workspace {
		if d == doc {
			workspace[i] = edited
		}
	}

	var decls []declaration
	keywordsToo := false
	switch parent := ident.Parent(); {
	case isDeclaredName(workspace, edited, ident) || declaringKeywords[prevToken(ident)]:
		// Naming something new.
		return nil
	case isFieldName(ident) && (parent.Kind() == "named_tuple_access_expression" || parent.Kind() == "named_tuple_lvalue"):
		decls = fieldsAfter(workspace, edited, parent.NamedChild(0))
	case isFieldName(ident) && parent.Kind() == "named_tuple_body" || parent.Kind() == "ERROR" && parent.Parent() != nil && parent.Parent().Kind() == "named_tuple_expression":
		// A field of a tuple value. After the first, as in
		// `(source = this, `, the new field is an ERROR.
		if t, ok := expectedType(workspace, edited, parent.Parent()); ok {
			decls = fieldsOf(workspace, t)
		}
	default:
		decls = namesInScope(workspace, edited, ident)
		keywordsToo = namespaceOf(ident) == nsValue
	}

	items := []protocol.CompletionItem{}
	seen := map[string]bool{}
	for _, d := range decls {
		// Inner names hide outer ones, and those come first.
		if seen[d.name] || strings.Contains(d.name, placeholder) {
			continue
		}
		seen[d.name] = true
		items = append(items, completionItem(d))
	}
	if keywordsToo {
		kind := protocol.CompletionItemKindKeyword
		for keyword := range keywords {
			sortText := "1" + keyword // after names
			items = append(items, protocol.CompletionItem{Label: keyword, Kind: &kind, SortText: &sortText})
		}
	}
	return items
}

// declaringKeywords are the keywords a new name follows. Half-typed
// declarations such as `var x` don't parse, so this catches those.
var declaringKeywords = map[string]bool{}

func init() {
	for _, k := range strings.Fields(`Lemma Theorem enum event eventset fun implementation interface
		invariant machine module param paramtest pure scenario spec state test type var`) {
		declaringKeywords[k] = true
	}
}

// prevToken returns the kind of the token before node, or "".
func prevToken(node *sitter.Node) string {
	if prev := node.PrevSibling(); prev != nil {
		return prev.Kind()
	}
	return ""
}

// namesInScope returns the declarations the name ident could refer to, in
// its namespace, innermost first.
func namesInScope(workspace []*Document, doc *Document, ident *sitter.Node) []declaration {
	ns := namespaceOf(ident)
	decls := []declaration{}
	for scope := ident.Parent(); scope != nil; scope = scope.Parent() {
		decls = append(decls, doc.scopeDeclarations(scope, ns)...)
	}
	if ns != nsState {
		decls = append(decls, globalDeclarationsIn(workspace, ns)...)
	}
	if isTestedKind(ident) {
		decls = append(decls, testedStates(workspace, doc, ident.Parent())...)
	}
	return decls
}

// isDeclaredName reports whether ident is the name in a declaration.
func isDeclaredName(workspace []*Document, doc *Document, ident *sitter.Node) bool {
	for _, d := range resolve(workspace, doc, ident) {
		if sameNode(d.node, ident) {
			return true
		}
	}
	return false
}

// fieldsAfter returns the fields that can follow `base.`.
func fieldsAfter(workspace []*Document, doc *Document, base *sitter.Node) []declaration {
	if spec, ok := specReference(workspace, doc, base); ok {
		return machineVariables(spec.doc, spec.node.Parent())
	}
	if t, ok := typeOf(workspace, doc, base); ok {
		return fieldsOf(workspace, t)
	}
	return nil
}

func completionItem(d declaration) protocol.CompletionItem {
	kind := protocol.CompletionItemKindVariable
	switch decl := d.node.Parent(); decl.Kind() {
	case "identifier_list":
		if owner := decl.Parent(); owner.Parent() != nil && owner.Parent().Kind() == "machine_entry" {
			kind = protocol.CompletionItemKindField
		}
	case "identifier_type":
		kind = protocol.CompletionItemKindField
	default:
		if spec, ok := symbolSpecs[decl.Kind()]; ok {
			kind = completionKinds[spec.kind]
		}
	}

	// The signature's first line, since clients show details on one line.
	detail, _, _ := strings.Cut(signature(d), "\n")
	sortText := "0" + d.name
	item := protocol.CompletionItem{Label: d.name, Kind: &kind, Detail: &detail, SortText: &sortText}
	if comment := docComment(d); comment != "" {
		item.Documentation = protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: comment}
	}
	return item
}
