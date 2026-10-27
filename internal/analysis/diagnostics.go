package analysis

import (
	"fmt"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

const diagnosticSource = "p-lsp"

// SyntaxDiagnostics reports the ERROR and MISSING nodes tree-sitter produced
// while recovering from syntax errors.
func SyntaxDiagnostics(doc *Document) []protocol.Diagnostic {
	diagnostics := []protocol.Diagnostic{}
	for _, e := range syntaxErrors(doc) {
		diagnostics = append(diagnostics, e.Diagnostic)
	}
	return diagnostics
}

// syntaxFixes returns the syntax errors with an obvious fix.
func syntaxFixes(doc *Document) []Fix {
	fixes := []Fix{}
	for _, e := range syntaxErrors(doc) {
		if len(e.Edits) > 0 {
			fixes = append(fixes, e)
		}
	}
	return fixes
}

// syntaxErrors returns doc's syntax errors, with edits for those whose
// cause is clear: a missing token such as `;`, or the trailing comma a
// one-field tuple needs.
func syntaxErrors(doc *Document) []Fix {
	errors := []Fix{}
	walkNodes(doc.Tree.RootNode(), func(n *sitter.Node) bool {
		switch {
		case n.IsError():
			if at, ok := missingSemicolon(doc, n); ok {
				errors = append(errors, insertion(doc, at, ";", "expected `;`"))
			} else {
				errors = append(errors, Fix{Diagnostic: syntaxDiagnostic(doc.Range(n), unexpectedMessage(doc, n))})
			}
			// Anything inside an ERROR node is part of the same mistake.
			return false
		case isMissingTupleComma(n):
			e := insertion(doc, n.StartByte(), ",", missingTupleCommaMessage(doc, n.Parent()))
			e.Title = "Add a trailing comma"
			errors = append(errors, e)
			return false
		case n.IsMissing() && !n.IsNamed():
			// An anonymous node's kind is its text.
			errors = append(errors, missingToken(doc, n))
			return false
		case n.IsMissing():
			// A missing name or expression, which there's no guessing.
			errors = append(errors, Fix{Diagnostic: syntaxDiagnostic(doc.Range(n), fmt.Sprintf("expected %s", strings.ReplaceAll(n.Kind(), "_", " ")))})
			return false
		}
		return n.HasError()
	})
	return errors
}

// missingToken reports the token tree-sitter found missing at n, with a fix
// that inserts it. The fix goes right after the token before, not after
// any comments between them as tree-sitter places it, and is offered only
// if it works. Tree-sitter's guess for a `;` can be a line late, so other
// places for it are tried too.
func missingToken(doc *Document, n *sitter.Node) Fix {
	text := n.Kind()
	message := fmt.Sprintf("expected `%s`", text)
	from := lineStart(doc, n.StartByte())
	candidates := []uint{n.StartByte()}
	if prev := tokenBefore(n); prev != nil {
		candidates = []uint{prev.EndByte(), n.StartByte()}
		from = lineStart(doc, prev.StartByte())
	}
	for _, at := range candidates {
		if fixesRegion(doc, at, text, from, n.EndByte()+1) {
			return insertion(doc, at, text, message)
		}
	}
	if text == ";" {
		if at, ok := missingSemicolon(doc, n); ok {
			return insertion(doc, at, text, message)
		}
	}
	return Fix{Diagnostic: syntaxDiagnostic(doc.Range(n), message)}
}

// insertion is a syntax error fixed by inserting text at offset.
func insertion(doc *Document, offset uint, text, message string) Fix {
	at := doc.Position(pointAt(doc, offset))
	rng := protocol.Range{Start: at, End: at}
	return Fix{
		Diagnostic: syntaxDiagnostic(rng, message),
		Title:      fmt.Sprintf("Insert `%s`", text),
		Edits:      []protocol.TextEdit{{Range: rng, NewText: text}},
	}
}

// maxSemicolonTries bounds how many places missingSemicolon tries a `;`.
const maxSemicolonTries = 8

// missingSemicolon reports whether the error err is there because a line
// doesn't end with a `;`, as in
//
//	event eFoo
//	event eBar;
//
// and returns where the `;` goes. Tree-sitter reports most missing
// semicolons itself, as MISSING nodes, but not between top-level
// declarations, where it wraps them in an ERROR instead. This tries a `;`
// at the end of each line from the one before err through err's own, and
// takes the first that makes the region parse.
func missingSemicolon(doc *Document, err *sitter.Node) (uint, bool) {
	// Start at the line of the last token before err, past any comments.
	from := lineStart(doc, err.StartByte())
	if prev := tokenBefore(err); prev != nil {
		from = lineStart(doc, prev.StartByte())
	}
	lines := lastTokensOfLines(doc, from, err.EndByte())
	tries := 0
	// Lines ending in `}` usually close a block, which takes no `;`, so
	// they're tried last; module expressions like `{ Client }` end in one.
	for _, afterBrace := range []bool{false, true} {
		for _, last := range lines {
			switch last.Kind() {
			case ";", "{", ",", "(", "[":
				continue
			case "}":
				if !afterBrace {
					continue
				}
			default:
				if afterBrace {
					continue
				}
			}
			if tries++; tries > maxSemicolonTries {
				return 0, false
			}
			if fixesRegion(doc, last.EndByte(), ";", from, err.EndByte()+1) {
				return last.EndByte(), true
			}
		}
	}
	return 0, false
}

// lastTokensOfLines returns the last token on each line between the byte
// offsets from and to, skipping comments.
func lastTokensOfLines(doc *Document, from, to uint) []*sitter.Node {
	var last []*sitter.Node
	walkNodes(doc.Tree.RootNode(), func(n *sitter.Node) bool {
		if n.EndByte() <= from || n.StartByte() >= to {
			return false
		}
		if n.ChildCount() > 0 {
			return true
		}
		if n.Kind() == "comment" || n.EndByte() == n.StartByte() {
			return false
		}
		if len(last) > 0 && last[len(last)-1].EndPosition().Row == n.EndPosition().Row {
			last[len(last)-1] = n
		} else {
			last = append(last, n)
		}
		return false
	})
	return last
}

// fixesRegion reports whether inserting text at offset leaves no syntax
// error between the byte offsets from and to (counted after the insertion).
func fixesRegion(doc *Document, offset uint, text string, from, to uint) bool {
	edited := make([]byte, 0, len(doc.Text)+len(text))
	edited = append(append(append(edited, doc.Text[:offset]...), text...), doc.Text[offset:]...)

	// Reparse incrementally from doc's tree, which is much faster than
	// starting over; text never contains a newline.
	old := doc.Tree.Clone()
	defer old.Close()
	at := pointAt(doc, offset)
	end := sitter.Point{Row: at.Row, Column: at.Column + uint(len(text))}
	old.Edit(&sitter.InputEdit{
		StartByte: offset, OldEndByte: offset, NewEndByte: offset + uint(len(text)),
		StartPosition: at, OldEndPosition: at, NewEndPosition: end,
	})
	parser := NewParser()
	defer parser.Close()
	tree := parser.Parse(edited, old)
	defer tree.Close()
	return !hasErrorBetween(tree.RootNode(), from, to+uint(len(text)))
}

// lineStart returns the offset of the start of the line offset is on.
func lineStart(doc *Document, offset uint) uint {
	return uint(doc.lineStarts[pointAt(doc, offset).Row])
}

// tokenBefore returns the token before n, skipping comments and the
// zero-width tokens tree-sitter inserts for missing ones.
func tokenBefore(n *sitter.Node) *sitter.Node {
	for {
		p := n.PrevSibling()
		for p == nil {
			if n = n.Parent(); n == nil {
				return nil
			}
			p = n.PrevSibling()
		}
		for p.ChildCount() > 0 {
			p = p.Child(p.ChildCount() - 1)
		}
		if p.Kind() != "comment" && p.EndByte() > p.StartByte() {
			return p
		}
		n = p
	}
}

// hasErrorBetween reports whether the tree under n has a syntax error that
// overlaps the bytes from start to end.
func hasErrorBetween(n *sitter.Node, start, end uint) bool {
	found := false
	walkNodes(n, func(c *sitter.Node) bool {
		if found || c.EndByte() < start || c.StartByte() > end {
			return false
		}
		if c.IsError() || c.IsMissing() {
			found = true
			return false
		}
		return c.HasError()
	})
	return found
}

// pointAt converts a byte offset in doc's text to a tree-sitter point.
func pointAt(doc *Document, offset uint) sitter.Point {
	row := 0
	for row+1 < len(doc.lineStarts) && uint(doc.lineStarts[row+1]) <= offset {
		row++
	}
	return sitter.Point{Row: uint(row), Column: offset - uint(doc.lineStarts[row])}
}

func unexpectedMessage(doc *Document, node *sitter.Node) string {
	text := strings.TrimSpace(node.Utf8Text(doc.Text))
	if line, _, found := strings.Cut(text, "\n"); found || len(line) > 40 {
		return "syntax error"
	}
	return fmt.Sprintf("syntax error: unexpected `%s`", text)
}

func syntaxDiagnostic(rng protocol.Range, message string) protocol.Diagnostic {
	severity := protocol.DiagnosticSeverityError
	source := diagnosticSource
	return protocol.Diagnostic{
		Range:    rng,
		Severity: &severity,
		Source:   &source,
		Message:  message,
	}
}
