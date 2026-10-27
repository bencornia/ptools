package server

import (
	"bytes"
	"errors"
	"unicode/utf8"

	"github.com/bencornia/pfmt/format"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// formatting formats a document with pfmt, indenting by the editor's tab
// size. Code with syntax errors is left alone, without an error, since
// editors that format on save would otherwise report it on every save.
func (s *Server) formatting(_ *glsp.Context, params *protocol.DocumentFormattingParams) ([]protocol.TextEdit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.docs[key(params.TextDocument.URI)]
	if !ok {
		return nil, nil
	}
	out, err := format.Source(doc.Text, format.Options{Indent: tabSize(params.Options)})
	switch {
	case errors.Is(err, format.ErrSyntax):
		return nil, nil
	case err != nil:
		return nil, err
	}
	if bytes.Equal(out, doc.Text) {
		return []protocol.TextEdit{}, nil
	}

	// Replace only the part that changed, which keeps editors' cursors and
	// undo steadier than replacing the whole document.
	old := doc.Text
	prefix := 0
	for prefix < len(old) && prefix < len(out) && old[prefix] == out[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(old)-prefix && suffix < len(out)-prefix && old[len(old)-1-suffix] == out[len(out)-1-suffix] {
		suffix++
	}
	// Don't split a character.
	for prefix > 0 && !utf8.RuneStart(old[prefix]) {
		prefix--
	}
	for suffix > 0 && !utf8.RuneStart(old[len(old)-suffix]) {
		suffix--
	}
	return []protocol.TextEdit{{
		Range: protocol.Range{
			Start: doc.PositionAt(uint(prefix)),
			End:   doc.PositionAt(uint(len(old) - suffix)),
		},
		NewText: string(out[prefix : len(out)-suffix]),
	}}, nil
}

// tabSize returns the indentation width the editor asked for, or 0 for
// pfmt's default.
func tabSize(options protocol.FormattingOptions) int {
	switch size := options[protocol.FormattingOptionTabSize].(type) {
	case float64:
		return int(size)
	case int:
		return size
	case protocol.UInteger:
		return int(size)
	}
	return 0
}
