package analysis

import (
	protocol "github.com/tliron/glsp/protocol_3_16"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// TestCase is a test declared in a document: its name, which p check
// --testcase takes, and the range of the declaration's first line.
type TestCase struct {
	Name  string
	Range protocol.Range
}

// TestCases returns the test cases declared in doc, safety and refinement
// tests both, in order. A parameterised test is one, named for all of its
// cases.
func TestCases(doc *Document) []TestCase {
	tests := []TestCase{}
	var visit func(n *sitter.Node)
	visit = func(n *sitter.Node) {
		for _, child := range namedChildren(n) {
			switch child.Kind() {
			case "test_declaration", "ERROR":
				visit(child)
			case "safety_test_declaration", "refinement_test_declaration":
				name := child.ChildByFieldName("test_name")
				if name == nil || name.IsMissing() {
					continue
				}
				start := doc.Position(child.StartPosition())
				tests = append(tests, TestCase{
					Name:  name.Utf8Text(doc.Text),
					Range: protocol.Range{Start: start, End: doc.Range(name).End},
				})
			}
		}
	}
	visit(doc.Tree.RootNode())
	return tests
}
