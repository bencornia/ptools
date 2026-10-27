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
	collectSyntaxErrors(doc, doc.Tree.RootNode(), &diagnostics)
	return diagnostics
}

func collectSyntaxErrors(doc *Document, node *sitter.Node, out *[]protocol.Diagnostic) {
	switch {
	case node.IsError():
		*out = append(*out, syntaxDiagnostic(doc.Range(node), unexpectedMessage(doc, node)))
		// Anything inside an ERROR node is part of the same mistake.
		return
	case node.IsMissing():
		*out = append(*out, syntaxDiagnostic(doc.Range(node), fmt.Sprintf("expected `%s`", node.Kind())))
		return
	case !node.HasError():
		return
	}

	for i := uint(0); i < node.ChildCount(); i++ {
		collectSyntaxErrors(doc, node.Child(i), out)
	}
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
