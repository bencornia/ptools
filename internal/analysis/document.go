// Package analysis turns P source text into editor features. It knows about
// tree-sitter and the P grammar, but nothing about the LSP transport.
package analysis

import (
	"sync"
	"unicode/utf16"
	"unicode/utf8"

	tree_sitter_p "github.com/bencornia/tree-sitter-p/bindings/go"
	protocol "github.com/tliron/glsp/protocol_3_16"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// NewParser returns a tree-sitter parser configured for P. Parsers are not
// safe for concurrent use.
func NewParser() *sitter.Parser {
	parser := sitter.NewParser()
	if err := parser.SetLanguage(sitter.NewLanguage(tree_sitter_p.Language())); err != nil {
		// Only happens if the generated parser's ABI doesn't match the
		// go-tree-sitter runtime, which is a build problem, not a runtime one.
		panic(err)
	}
	return parser
}

// Document is an open P source file and its syntax tree.
type Document struct {
	URI     string
	Version int32
	Text    []byte
	Tree    *sitter.Tree

	// lineStarts[i] is the byte offset where line i begins.
	lineStarts []int

	// globals caches the top-level declarations in each namespace, which
	// name resolution looks through for most names.
	globals [nsCount]struct {
		once  sync.Once
		decls []declaration
	}
	// scopes caches the declarations in each scope it's asked about.
	scopesMu sync.Mutex
	scopes   map[scopeKey][]declaration

	// uses caches how many identifiers spell each name; see nameUses.
	uses struct {
		once   sync.Once
		counts map[string]uses
	}

	// parents caches each node's parent; see parent.
	parentsMu sync.Mutex
	parents   map[uintptr]*sitter.Node
}

// walk calls visit on the nodes of d's tree depth first, skipping the
// children of nodes visit returns false for, and caches each node's parent
// on the way down, which saves parent searching for it.
func (d *Document) walk(visit func(*sitter.Node) bool) {
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if !visit(n) {
			return
		}
		count := n.ChildCount()
		if count == 0 {
			return
		}
		children := make([]*sitter.Node, count)
		d.parentsMu.Lock()
		if d.parents == nil {
			d.parents = map[uintptr]*sitter.Node{}
		}
		for i := range count {
			children[i] = n.Child(i)
			d.parents[children[i].Id()] = n
		}
		d.parentsMu.Unlock()
		for _, c := range children {
			walk(c)
		}
	}
	walk(d.Tree.RootNode())
}

// parent returns n's parent, n being a node of d's tree. Tree-sitter finds a
// node's parent by searching down from the root, so walking up from a node
// costs time proportional to the square of its depth; name resolution does
// that for every name, and ancestors are shared, so they're cached.
func (d *Document) parent(n *sitter.Node) *sitter.Node {
	d.parentsMu.Lock()
	defer d.parentsMu.Unlock()
	id := n.Id()
	if p, ok := d.parents[id]; ok {
		return p
	}
	if d.parents == nil {
		d.parents = map[uintptr]*sitter.Node{}
	}
	p := n.Parent()
	d.parents[id] = p
	return p
}

type scopeKey struct {
	id uintptr
	ns namespace
}

// Parse builds a Document from the full text of a file.
func Parse(parser *sitter.Parser, uri string, version int32, text string) *Document {
	src := []byte(text)
	lineStarts := []int{0}
	for i, b := range src {
		if b == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}
	return &Document{
		URI:        uri,
		Version:    version,
		Text:       src,
		Tree:       parser.Parse(src, nil),
		lineStarts: lineStarts,
	}
}

// Close frees the syntax tree, which is allocated in C.
func (d *Document) Close() {
	d.Tree.Close()
}

// Position converts a tree-sitter point (row, byte column) to an LSP position
// (line, UTF-16 column).
func (d *Document) Position(p sitter.Point) protocol.Position {
	row := int(p.Row)
	if row >= len(d.lineStarts) {
		row = len(d.lineStarts) - 1
	}
	start := d.lineStarts[row]
	end := min(start+int(p.Column), len(d.Text))

	col := 0
	for line := d.Text[start:end]; len(line) > 0; {
		r, size := utf8.DecodeRune(line)
		col += max(utf16.RuneLen(r), 1)
		line = line[size:]
	}
	return protocol.Position{Line: protocol.UInteger(row), Character: protocol.UInteger(col)}
}

// Point converts an LSP position (line, UTF-16 column) to a tree-sitter point
// (row, byte column). Positions past the end of a line clamp to its end.
func (d *Document) Point(pos protocol.Position) sitter.Point {
	row := min(int(pos.Line), len(d.lineStarts)-1)
	start := d.lineStarts[row]
	end := len(d.Text)
	if row+1 < len(d.lineStarts) {
		end = d.lineStarts[row+1] - 1 // the '\n'
	}

	offset, units := start, 0
	for offset < end && units < int(pos.Character) {
		r, size := utf8.DecodeRune(d.Text[offset:end])
		units += max(utf16.RuneLen(r), 1)
		offset += size
	}
	return sitter.Point{Row: uint(row), Column: uint(offset - start)}
}

// offset converts an LSP position to a byte offset in the text.
func (d *Document) offset(pos protocol.Position) uint {
	p := d.Point(pos)
	return uint(d.lineStarts[p.Row]) + p.Column
}

// lineLength returns the length in bytes of line row, without its line
// ending.
func (d *Document) lineLength(row uint) uint {
	start, end := d.lineStarts[row], len(d.Text)
	if int(row)+1 < len(d.lineStarts) {
		end = d.lineStarts[row+1] - 1 // the '\n'
	}
	if end > start && d.Text[end-1] == '\r' {
		end--
	}
	return uint(end - start)
}

// PositionAt converts a byte offset in the text to an LSP position.
func (d *Document) PositionAt(offset uint) protocol.Position {
	return d.Position(pointAt(d, offset))
}

// WordRange returns the range of the token at pos, such as a name or keyword,
// or of the single character there if there's no token, for underlining an
// error the compiler reported at a position.
func (d *Document) WordRange(pos protocol.Position) protocol.Range {
	point := d.Point(pos)
	if n := d.Tree.RootNode().DescendantForPointRange(point, point); n != nil && n.ChildCount() == 0 &&
		n.StartPosition() == point && n.EndByte() > n.StartByte() {
		return d.Range(n)
	}
	end := pos
	end.Character++
	return protocol.Range{Start: pos, End: end}
}

// Range returns the LSP range covered by a node.
func (d *Document) Range(n *sitter.Node) protocol.Range {
	return protocol.Range{
		Start: d.Position(n.StartPosition()),
		End:   d.Position(n.EndPosition()),
	}
}
