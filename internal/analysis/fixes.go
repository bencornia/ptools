package analysis

import (
	"fmt"

	protocol "github.com/tliron/glsp/protocol_3_16"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Fix is a problem in a document and the edits that fix it.
type Fix struct {
	Diagnostic protocol.Diagnostic
	Title      string
	Edits      []protocol.TextEdit
}

// Diagnostics returns doc's problems: its syntax errors, and mistakes found
// through types that the compiler would otherwise only report on save.
func Diagnostics(workspace []*Document, doc *Document) []protocol.Diagnostic {
	diagnostics := SyntaxDiagnostics(doc)
	// A problem with several fixes comes once for each.
	seen := map[protocol.Range]map[string]bool{}
	for _, f := range semanticFixes(workspace, doc) {
		d := f.Diagnostic
		if seen[d.Range][d.Message] {
			continue
		}
		if seen[d.Range] == nil {
			seen[d.Range] = map[string]bool{}
		}
		seen[d.Range][d.Message] = true
		diagnostics = append(diagnostics, d)
	}
	return diagnostics
}

// Fixes returns the problems in doc that can be fixed automatically. Each
// one's diagnostic is also among Diagnostics.
func Fixes(workspace []*Document, doc *Document) []Fix {
	fixes := syntaxFixes(doc)
	for _, f := range semanticFixes(workspace, doc) {
		if len(f.Edits) > 0 {
			fixes = append(fixes, f)
		}
	}
	return fixes
}

// semanticFixes finds the mistakes that parse but that the compiler would
// reject. Some have no fix, only a diagnostic, and some have several fixes,
// each with the same diagnostic. The checks all run in one walk of the
// tree, which on a large file is much of their cost.
func semanticFixes(workspace []*Document, doc *Document) []Fix {
	fixes := []Fix{}
	doc.walk(func(n *sitter.Node) bool {
		switch n.Kind() {
		case "ERROR":
			// Already reported as a syntax error.
			return false
		case "paren_expression":
			if f, ok := parenthesizedNotTuple(workspace, doc, n); ok {
				fixes = append(fixes, f)
			}
		case "receive_statement":
			if f, ok := emptyReceive(doc, n); ok {
				fixes = append(fixes, f)
			}
		case "identifier":
			if eventReference(doc, n) != nil {
				fixes = append(fixes, undefinedEvent(workspace, doc, n)...)
			} else {
				fixes = append(fixes, undefinedName(workspace, doc, n)...)
			}
		}
		return true
	})
	return fixes
}

// emptyReceive reports a `receive` statement with no cases, which P
// rejects. The grammar allows it so that editors can indent the first case
// as it's typed.
func emptyReceive(doc *Document, receive *sitter.Node) (Fix, bool) {
	if firstChildOfKind(receive, "recv_case") != nil {
		return Fix{}, false
	}
	return Fix{Diagnostic: syntaxDiagnostic(doc.Range(receive.Child(0)), "`receive` needs at least one `case`")}, true
}

// isMissingTupleComma reports whether n is the comma tree-sitter expected
// after a tuple's only field, as in `(a = 1)`.
func isMissingTupleComma(n *sitter.Node) bool {
	return n.IsMissing() && n.Kind() == "," && n.Parent() != nil &&
		(n.Parent().Kind() == "named_tuple_body" || n.Parent().Kind() == "unnamed_tuple_body")
}

func missingTupleCommaMessage(doc *Document, body *sitter.Node) string {
	const message = "a tuple with one field needs a trailing comma"
	if text := body.Utf8Text(doc.Text); len(text) <= 40 {
		return fmt.Sprintf("%s: `(%s,)`", message, text)
	}
	return message
}

// parenthesizedNotTuple reports a value in parentheses, like `(1)`, where
// a one-element tuple is expected: P reads it as the value itself, not a
// tuple.
func parenthesizedNotTuple(workspace []*Document, doc *Document, paren *sitter.Node) (Fix, bool) {
	inner := paren.NamedChild(0)
	if inner == nil || !isNotTuple(workspace, doc, inner) {
		return Fix{}, false
	}
	want, ok := expectedType(workspace, doc, paren)
	if !ok {
		return Fix{}, false
	}
	if want, ok = underlying(workspace, want); !ok {
		return Fix{}, false
	}

	// Insert after `(` (a field name, for a named tuple) and before `)`.
	open, close := doc.Range(paren).Start, doc.Range(paren).End
	open.Character++
	close.Character--
	prefix := ""
	switch want.node.Kind() {
	case "tuple_type":
		if len(want.node.ChildrenByFieldName("tup_types", want.node.Walk())) != 1 {
			return Fix{}, false
		}
	case "named_tuple_type":
		fields := tupleFields(want)
		if len(fields) != 1 {
			return Fix{}, false
		}
		prefix = fields[0].name + " = "
	default:
		return Fix{}, false
	}

	value := inner.Utf8Text(doc.Text)
	fixed := fmt.Sprintf("(%s%s,)", prefix, value)
	severity := protocol.DiagnosticSeverityError
	source := diagnosticSource
	edits := []protocol.TextEdit{{Range: protocol.Range{Start: close, End: close}, NewText: ","}}
	if prefix != "" {
		edits = append([]protocol.TextEdit{{Range: protocol.Range{Start: open, End: open}, NewText: prefix}}, edits...)
	}
	message := fmt.Sprintf("this is `%s` in parentheses, not a tuple; a tuple with one field needs a trailing comma: `%s`", value, fixed)
	if len(value) > 40 {
		message = "this is a value in parentheses, not a tuple; a tuple with one field needs a trailing comma"
	}
	return Fix{
		Diagnostic: protocol.Diagnostic{Range: doc.Range(paren), Severity: &severity, Source: &source, Message: message},
		Title:      fmt.Sprintf("Make it a tuple: `%s`", fixed),
		Edits:      edits,
	}, true
}

// isNotTuple reports whether expr is known not to be a tuple, so `(expr)`
// can't be meant as one: a literal, arithmetic, or a name declared with a
// type that isn't a tuple. Anything it can't tell about is assumed to be a
// tuple, which is never reported.
func isNotTuple(workspace []*Document, doc *Document, expr *sitter.Node) bool {
	switch expr.Kind() {
	case "int_literal", "float_literal", "bool_literal", "string_literal", "string_expression", "null_literal",
		"bin_expression", "unary_expression":
		return true
	case "identifier":
		decls := resolve(workspace, doc, expr)
		if len(decls) != 1 {
			return false
		}
		if t, ok := declaredType(decls[0]); ok {
			u, ok := underlying(workspace, t)
			return ok && u.node.Kind() != "tuple_type" && u.node.Kind() != "named_tuple_type"
		}
		// A primitive type, which has no node of its own; `any` and `data`
		// can hold tuples.
		decl := decls[0].parent()
		if decl.Kind() == "identifier_list" {
			decl = decl.Parent()
		}
		if decl.Kind() != "var_declaration" && decl.Kind() != "function_param" {
			return false
		}
		for i := uint(0); i < decl.ChildCount(); i++ {
			if kind := decl.Child(i).Kind(); builtinTypes[kind] {
				return kind != "any" && kind != "data"
			}
		}
	}
	return false
}

// walkNodes calls visit on n and its descendants, depth first, skipping the
// children of nodes visit returns false for.
func walkNodes(n *sitter.Node, visit func(*sitter.Node) bool) {
	if !visit(n) {
		return
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		walkNodes(n.Child(i), visit)
	}
}
