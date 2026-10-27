package analysis

import (
	"bytes"
	"cmp"
	"slices"

	protocol "github.com/tliron/glsp/protocol_3_16"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// References returns every identifier in candidates that refers to the same
// declaration as the identifier at pos in doc, sorted by location.
// workspaceOf gives the documents each document is resolved against, as for
// Definition. includeDeclaration adds the declarations themselves.
func References(candidates []*Document, workspaceOf func(*Document) []*Document, doc *Document, pos protocol.Position, includeDeclaration bool) []protocol.Location {
	ident := identifierAt(doc, pos)
	if ident == nil {
		return nil
	}
	targets := resolve(workspaceOf(doc), doc, ident)
	if len(targets) == 0 {
		return nil
	}

	locs := []protocol.Location{}
	for _, r := range referencesTo(candidates, workspaceOf, targets, includeDeclaration) {
		locs = append(locs, protocol.Location{URI: r.doc.URI, Range: r.doc.Range(r.node)})
	}
	slices.SortFunc(locs, func(a, b protocol.Location) int {
		return cmp.Or(
			cmp.Compare(a.URI, b.URI),
			cmp.Compare(a.Range.Start.Line, b.Range.Start.Line),
			cmp.Compare(a.Range.Start.Character, b.Range.Start.Character),
		)
	})
	return locs
}

// reference is an identifier that refers to a declaration, or the
// declaration's own name.
type reference struct {
	doc  *Document
	node *sitter.Node
}

// nodeKey identifies a node. Nodes from different parses of a file are never
// compared: every document here comes from the same snapshot.
type nodeKey struct {
	doc   *Document
	start uint
}

func keyOf(doc *Document, node *sitter.Node) nodeKey {
	return nodeKey{doc, node.StartByte()}
}

// referencesTo returns the identifiers in candidates that resolve to one of
// targets, and the targets' own names if includeDeclaration.
func referencesTo(candidates []*Document, workspaceOf func(*Document) []*Document, targets []declaration, includeDeclaration bool) []reference {
	isTarget := map[nodeKey]bool{}
	names := map[string]bool{}
	for _, t := range targets {
		isTarget[keyOf(t.doc, t.node)] = true
		names[t.name] = true
	}

	refs := []reference{}
	seen := map[nodeKey]bool{}
	add := func(d *Document, n *sitter.Node) {
		if k := keyOf(d, n); !seen[k] {
			seen[k] = true
			refs = append(refs, reference{d, n})
		}
	}
	if includeDeclaration {
		for _, t := range targets {
			add(t.doc, t.node)
		}
	}

	for _, candidate := range candidates {
		// Only identifiers spelled like a target can refer to it, so skip
		// files that don't mention one, and resolve only those identifiers.
		if !mentionsAny(candidate.Text, names) {
			continue
		}
		workspace := workspaceOf(candidate)
		forEachIdentifier(candidate.Tree.RootNode(), func(n *sitter.Node) {
			if !names[n.Utf8Text(candidate.Text)] || isTarget[keyOf(candidate, n)] {
				return
			}
			for _, d := range resolve(workspace, candidate, n) {
				if isTarget[keyOf(d.doc, d.node)] {
					add(candidate, n)
					return
				}
			}
		})
	}
	return refs
}

func mentionsAny(text []byte, names map[string]bool) bool {
	for name := range names {
		if bytes.Contains(text, []byte(name)) {
			return true
		}
	}
	return false
}

func forEachIdentifier(node *sitter.Node, f func(*sitter.Node)) {
	if node.Kind() == "identifier" {
		f(node)
		return
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		forEachIdentifier(node.Child(i), f)
	}
}
