package analysis

import (
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// FoldingRanges returns the regions of doc an editor can fold: everything
// between `{` and `}` that spans lines, block comments, and runs of line
// comments.
func FoldingRanges(doc *Document) []protocol.FoldingRange {
	folds := []protocol.FoldingRange{}
	comment := string(protocol.FoldingRangeKindComment)

	// A run of `//` comments, each on its own line, folds as one.
	var run []*sitter.Node
	endRun := func() {
		if len(run) > 1 {
			folds = append(folds, protocol.FoldingRange{
				StartLine: protocol.UInteger(run[0].StartPosition().Row),
				EndLine:   protocol.UInteger(run[len(run)-1].EndPosition().Row),
				Kind:      &comment,
			})
		}
		run = nil
	}

	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n.Kind() == "comment" {
			start, end := n.StartPosition(), n.EndPosition()
			text := n.Utf8Text(doc.Text)
			if !strings.HasPrefix(text, "//") {
				endRun()
				if end.Row > start.Row {
					folds = append(folds, protocol.FoldingRange{
						StartLine: protocol.UInteger(start.Row),
						EndLine:   protocol.UInteger(end.Row),
						Kind:      &comment,
					})
				}
				return
			}
			if len(run) > 0 && (run[len(run)-1].EndPosition().Row+1 != start.Row || !aloneOnLine(doc, n)) {
				endRun()
			}
			if aloneOnLine(doc, n) {
				run = append(run, n)
			}
			return
		}
		if n.ChildCount() == 0 {
			// Code between comments ends a run.
			endRun()
			return
		}

		// Fold from the `{` line, leaving the `}` line showing.
		if open, close := firstTokenOfKind(n, "{"), lastTokenOfKind(n, "}"); open != nil && close != nil {
			start, end := open.StartPosition().Row, close.StartPosition().Row
			if end > start+1 {
				folds = append(folds, protocol.FoldingRange{
					StartLine: protocol.UInteger(start),
					EndLine:   protocol.UInteger(end - 1),
				})
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(doc.Tree.RootNode())
	endRun()
	return folds
}

// aloneOnLine reports whether only whitespace comes before n on its line.
func aloneOnLine(doc *Document, n *sitter.Node) bool {
	start := n.StartPosition()
	lineStart := doc.lineStarts[start.Row]
	return strings.TrimSpace(string(doc.Text[lineStart:lineStart+int(start.Column)])) == ""
}

func lastTokenOfKind(node *sitter.Node, kind string) *sitter.Node {
	for i := int(node.ChildCount()) - 1; i >= 0; i-- {
		if child := node.Child(uint(i)); child.Kind() == kind {
			return child
		}
	}
	return nil
}

// SelectionRanges returns, for each position, the ranges an editor's
// "expand selection" steps through: the token there, then each enclosing
// syntax node that covers more.
func SelectionRanges(doc *Document, positions []protocol.Position) []protocol.SelectionRange {
	selections := make([]protocol.SelectionRange, 0, len(positions))
	for _, pos := range positions {
		point := doc.Point(pos)
		var chain []protocol.Range
		for n := doc.Tree.RootNode().DescendantForPointRange(point, point); n != nil; n = n.Parent() {
			rng := doc.Range(n)
			if len(chain) == 0 || rng != chain[len(chain)-1] {
				chain = append(chain, rng)
			}
		}

		// Link them innermost first, each to the next one out.
		var parent *protocol.SelectionRange
		for i := len(chain) - 1; i > 0; i-- {
			parent = &protocol.SelectionRange{Range: chain[i], Parent: parent}
		}
		selections = append(selections, protocol.SelectionRange{Range: chain[0], Parent: parent})
	}
	return selections
}
