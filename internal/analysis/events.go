package analysis

import (
	"fmt"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// undefinedEventFixes finds events used but declared nowhere, and offers to
// declare each, with a payload type guessed from how it's used.
func undefinedEventFixes(workspace []*Document, doc *Document) []Fix {
	fixes := []Fix{}
	walkNodes(doc.Tree.RootNode(), func(n *sitter.Node) bool {
		event := eventReference(n)
		if event == nil || len(resolve(workspace, doc, event)) > 0 {
			return true
		}
		name := event.Utf8Text(doc.Text)
		declaration := "event " + name + ";"
		if payload, ok := payloadType(workspace, doc, event); ok {
			declaration = fmt.Sprintf("event %s: %s;", name, payload)
		}
		severity := protocol.DiagnosticSeverityError
		source := diagnosticSource
		fixes = append(fixes, Fix{
			Diagnostic: protocol.Diagnostic{
				Range:    doc.Range(event),
				Severity: &severity,
				Source:   &source,
				Message:  fmt.Sprintf("undefined event `%s`", name),
			},
			Title: fmt.Sprintf("Declare `%s`", declaration),
			Edits: []protocol.TextEdit{declarationEdit(doc, declaration)},
		})
		return true
	})
	return fixes
}

// eventReference returns the event name n is, if n is an identifier where an
// event goes: in `on`, `case`, `defer`, `ignore`, `receives`, `sends`,
// `observes` and event set lists, or the event of a `send`, `raise` or
// `announce`.
func eventReference(n *sitter.Node) *sitter.Node {
	if n.Kind() != "identifier" {
		return nil
	}
	parent := n.Parent()
	switch parent.Kind() {
	case "event_id", "non_default_event":
		return n
	case "send_statement":
		// send machine, event, payload...
		if comma := firstTokenOfKind(parent, ","); comma != nil && sameNode(comma.NextSibling(), n) {
			return n
		}
	case "raise_statement", "announce_statement":
		if parent.ChildCount() > 1 && sameNode(parent.Child(1), n) {
			return n
		}
	}
	return nil
}

// payloadType guesses the payload type of the event event refers to: the
// type of a handler's parameter, or of what a send, raise or announce
// passes. It returns false if there's no payload, and "any" for one it
// can't tell the type of.
func payloadType(workspace []*Document, doc *Document, event *sitter.Node) (string, bool) {
	parent := event.Parent()
	if parent.Kind() == "event_id" {
		// on e do (p: T), on e goto S with (p: T), case e: (p: T)
		handler := firstChildOfKind(parent.Parent().Parent(), "anon_event_handler")
		if handler == nil {
			return "", false
		}
		if param := firstChildOfKind(handler, "function_param"); param != nil {
			return paramTypeText(doc, param)
		}
		return "", false
	}
	payload := firstChildOfKind(parent, "rvalue_list")
	if payload == nil {
		return "", false
	}
	values := namedChildren(payload)
	types := make([]string, 0, len(values))
	for _, v := range values {
		t, ok := expressionTypeText(workspace, doc, v)
		if !ok {
			return "any", true
		}
		types = append(types, t)
	}
	if len(types) == 1 {
		return types[0], true
	}
	// send m, e, a, b sends the tuple (a, b).
	return "(" + strings.Join(types, ", ") + ")", true
}

// expressionTypeText returns the type of a simple expression as P source:
// a literal, `this`, a declared name, or a tuple of those.
func expressionTypeText(workspace []*Document, doc *Document, expr *sitter.Node) (string, bool) {
	if expr.Kind() == "rvalue" || expr.Kind() == "paren_expression" {
		if expr.NamedChildCount() == 0 {
			// `this`, which has no node of its own.
			if strings.TrimSpace(expr.Utf8Text(doc.Text)) == "this" {
				return "machine", true
			}
			return "", false
		}
		return expressionTypeText(workspace, doc, expr.NamedChild(0))
	}
	switch expr.Kind() {
	case "int_literal":
		return "int", true
	case "bool_literal":
		return "bool", true
	case "float_literal":
		return "float", true
	case "string_literal", "formatted_string", "string_expression":
		return "string", true
	case "identifier":
		if decls := resolve(workspace, doc, expr); len(decls) == 1 {
			return declaredTypeText(decls[0])
		}
	case "named_tuple_expression":
		body := firstChildOfKind(expr, "named_tuple_body")
		if body == nil {
			return "", false
		}
		fields := []string{}
		children := namedChildren(body)
		for i := 0; i+1 < len(children); i += 2 {
			t, ok := expressionTypeText(workspace, doc, children[i+1])
			if !ok {
				return "", false
			}
			fields = append(fields, children[i].Utf8Text(doc.Text)+": "+t)
		}
		return "(" + strings.Join(fields, ", ") + ")", true
	case "unnamed_tuple_expression":
		body := firstChildOfKind(expr, "unnamed_tuple_body")
		if body == nil {
			return "", false
		}
		elements := []string{}
		for _, v := range namedChildren(body) {
			t, ok := expressionTypeText(workspace, doc, v)
			if !ok {
				return "", false
			}
			elements = append(elements, t)
		}
		return "(" + strings.Join(elements, ", ") + ")", true
	}
	return "", false
}

// declaredTypeText returns the type a variable or parameter is declared
// with, as written.
func declaredTypeText(d declaration) (string, bool) {
	decl := d.node.Parent()
	switch decl.Kind() {
	case "function_param":
		return paramTypeText(d.doc, decl)
	case "identifier_list":
		owner := decl.Parent()
		colon, semicolon := firstTokenOfKind(owner, ":"), firstTokenOfKind(owner, ";")
		if colon == nil || semicolon == nil {
			return "", false
		}
		return strings.TrimSpace(string(d.doc.Text[colon.EndByte():semicolon.StartByte()])), true
	}
	return "", false
}

// paramTypeText returns the type in `name: type`.
func paramTypeText(doc *Document, param *sitter.Node) (string, bool) {
	colon := firstTokenOfKind(param, ":")
	if colon == nil {
		return "", false
	}
	return strings.TrimSpace(string(doc.Text[colon.EndByte():param.EndByte()])), true
}

// declarationEdit inserts a top-level declaration into doc: after its last
// event declaration if it has one, and otherwise before its first
// declaration and the comments above it.
func declarationEdit(doc *Document, declaration string) protocol.TextEdit {
	root := doc.Tree.RootNode()
	var lastEvent, first *sitter.Node
	for _, n := range namedChildren(root) {
		if n.Kind() == "event_declaration" {
			lastEvent = n
		}
		if first == nil && n.Kind() != "comment" {
			first = n
		}
	}
	if lastEvent != nil {
		at := doc.Range(lastEvent).End
		return protocol.TextEdit{Range: protocol.Range{Start: at, End: at}, NewText: "\n" + declaration}
	}
	if first == nil {
		at := doc.Position(root.EndPosition())
		return protocol.TextEdit{Range: protocol.Range{Start: at, End: at}, NewText: declaration + "\n"}
	}
	// Keep a declaration's comments attached to it.
	for prev := first.PrevNamedSibling(); prev != nil && prev.Kind() == "comment" &&
		prev.EndPosition().Row+1 >= first.StartPosition().Row; prev = prev.PrevNamedSibling() {
		first = prev
	}
	at := protocol.Position{Line: protocol.UInteger(first.StartPosition().Row)}
	return protocol.TextEdit{Range: protocol.Range{Start: at, End: at}, NewText: declaration + "\n\n"}
}
