package analysis

import (
	"fmt"
	"path"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Hover limits, so a long invariant or type doesn't fill the screen.
const (
	maxSignatureLines = 12
	maxSignatureBytes = 800
)

// Hover describes the declarations the identifier at pos refers to: each
// one's signature, its doc comment, and where it is if that's another file.
func Hover(workspace []*Document, doc *Document, pos protocol.Position) *protocol.Hover {
	ident := identifierAt(doc, pos)
	if ident == nil {
		return nil
	}
	decls := resolve(workspace, doc, ident)
	if len(decls) == 0 {
		return nil
	}

	sections := make([]string, 0, len(decls))
	for _, d := range decls {
		var b strings.Builder
		fmt.Fprintf(&b, "```p\n%s\n```", signature(d))
		if comment := docComment(d); comment != "" {
			b.WriteString("\n\n" + comment)
		}
		if d.doc != doc {
			fmt.Fprintf(&b, "\n\nDeclared in `%s`, line %d", path.Base(d.doc.URI), d.node.StartPosition().Row+1)
		}
		sections = append(sections, b.String())
	}

	rng := doc.Range(ident)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.MarkupKindMarkdown,
			Value: strings.Join(sections, "\n\n---\n\n"),
		},
		Range: &rng,
	}
}

// signature renders a declaration as P source: the whole declaration when
// it's short by nature, or its header without a body.
func signature(d declaration) string {
	decl := d.parent()
	text := func(n *sitter.Node) string { return n.Utf8Text(d.doc.Text) }

	switch decl.Kind() {
	case "identifier_list":
		// One of `var a, b: T;` or `param a, b: T;`.
		owner := decl.Parent()
		keyword := "var"
		if owner.Kind() == "global_param_declaration" {
			keyword = "param"
		}
		// The type is whatever comes between `:` and `;`. Primitive types
		// such as int have no node of their own, so use the text.
		colon, semicolon := firstTokenOfKind(owner, ":"), firstTokenOfKind(owner, ";")
		if colon == nil || semicolon == nil {
			return clip(text(owner))
		}
		typ := strings.TrimSpace(string(d.doc.Text[colon.EndByte():semicolon.StartByte()]))
		return clip(fmt.Sprintf("%s %s: %s", keyword, text(d.node), typ))
	case "impl_machine_declaration", "spec_machine_declaration", "scenario_machine_declaration":
		return header(d.doc, decl, firstChildOfKind(decl, "machine_body"))
	case "p_function_declaration":
		return header(d.doc, decl, firstChildOfKind(decl, "function_body"))
	case "state_declaration", "invariant_group_declaration", "proof_block_declaration":
		return header(d.doc, decl, firstTokenOfKind(decl, "{"))
	case "enum_elem", "numbered_enum_elem":
		// Show the whole enum, so the element's siblings and type are there.
		return clip(text(decl.Parent().Parent()))
	case "invariant_declaration":
		if decl.Parent() != nil && decl.Parent().Kind() == "invariant_group_declaration" {
			// Referenced as <group>_<name>.
			return clip(d.name + ":" + strings.TrimPrefix(text(decl), "invariant "+text(d.node)+":"))
		}
	}
	return clip(text(decl))
}

// header returns a declaration's text up to body, without trailing space.
func header(doc *Document, decl, body *sitter.Node) string {
	if body == nil {
		return clip(decl.Utf8Text(doc.Text))
	}
	return clip(strings.TrimSpace(string(doc.Text[decl.StartByte():body.StartByte()])))
}

// firstTokenOfKind is firstChildOfKind for anonymous children, such as "{".
func firstTokenOfKind(node *sitter.Node, kind string) *sitter.Node {
	for i := uint(0); i < node.ChildCount(); i++ {
		if child := node.Child(i); child.Kind() == kind {
			return child
		}
	}
	return nil
}

// clip shortens text to the hover limits.
func clip(text string) string {
	lines := strings.Split(text, "\n")
	clipped := len(lines) > maxSignatureLines
	if clipped {
		lines = lines[:maxSignatureLines]
	}
	text = strings.Join(lines, "\n")
	if len(text) > maxSignatureBytes {
		text, clipped = text[:maxSignatureBytes], true
	}
	if clipped {
		text += " …"
	}
	return text
}

// docComment returns the comment directly above a top-level or machine-level
// declaration, with its comment markers removed. Comments separated from the
// declaration by a blank line, or after code on their line, don't count.
func docComment(d declaration) string {
	statement := declaringStatement(d.node)
	if statement == nil {
		return ""
	}

	var comments []string
	next := statement
	for prev := next.PrevSibling(); prev != nil && prev.Kind() == "comment"; prev = prev.PrevSibling() {
		if prev.EndPosition().Row+1 < next.StartPosition().Row {
			break
		}
		if strings.TrimSpace(string(d.doc.Text[lineStart(d.doc, prev.StartByte()):prev.StartByte()])) != "" {
			// A comment after the code on the line above, like an
			// asciidoc callout, is about that line.
			break
		}
		comments = append([]string{commentText(prev.Utf8Text(d.doc.Text))}, comments...)
		next = prev
	}
	return strings.TrimSpace(strings.Join(comments, "\n"))
}

// declaringStatement returns the top-level or machine-level statement that
// declares name, which is where a doc comment would be. Locals, parameters,
// tuple fields and enum elements have none.
func declaringStatement(name *sitter.Node) *sitter.Node {
	decl := name.Parent()
	if decl.Kind() == "identifier_list" {
		decl = decl.Parent()
	}
	switch decl.Kind() {
	case "function_param", "identifier_type", "enum_elem", "numbered_enum_elem":
		return nil
	}
	for n := decl; n.Parent() != nil; n = n.Parent() {
		switch n.Parent().Kind() {
		case "program", "machine_body", "invariant_group_declaration":
			return n
		case "function_body":
			// A local variable.
			return nil
		}
	}
	return nil
}

// commentText strips the markers from a // or /* */ comment.
func commentText(comment string) string {
	if rest, ok := strings.CutPrefix(comment, "//"); ok {
		return strings.TrimSpace(rest)
	}
	comment = strings.TrimSuffix(strings.TrimPrefix(comment, "/*"), "*/")
	lines := strings.Split(comment, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		lines[i] = strings.TrimSpace(strings.TrimPrefix(line, "*"))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
