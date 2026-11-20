package server

import (
	"path/filepath"
	"testing"

	"github.com/bencornia/ptools/internal/format"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// applyEdits applies non-overlapping edits to text, as an editor would.
func applyEdits(t *testing.T, s *Server, path string, edits []protocol.TextEdit) string {
	t.Helper()
	doc := s.docs[path]
	text := string(doc.Text)
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		start, end := offsetOf(text, e.Range.Start), offsetOf(text, e.Range.End)
		text = text[:start] + e.NewText + text[end:]
	}
	return text
}

// offsetOf converts an LSP position to a byte offset in ASCII text.
func offsetOf(text string, pos protocol.Position) int {
	line, offset := 0, 0
	for offset < len(text) && line < int(pos.Line) {
		if text[offset] == '\n' {
			line++
		}
		offset++
	}
	return offset + int(pos.Character)
}

func TestFormatting(t *testing.T) {
	messy := "event e;\nmachine M {\nvar x : int;\n  start state S {\n      entry { x=x+1; }\n  }\n}\n"
	s, root := start(t, map[string]string{"App/App.pproj": appProject, "App/PSrc/Main.p": "event e;\n"}, client{})
	path := filepath.Join(root, "App/PSrc/Main.p")
	uri := pathToURI(path)
	if err := s.didOpen(fakeContext(nil), &protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{
		URI: uri, LanguageID: "p", Version: 1, Text: messy,
	}}); err != nil {
		t.Fatal(err)
	}

	edits, err := s.formatting(fakeContext(nil), &protocol.DocumentFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Options:      protocol.FormattingOptions{"tabSize": float64(4), "insertSpaces": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := format.Source([]byte(messy), format.Options{Indent: 4})
	if got := applyEdits(t, s, key(uri), edits); got != string(want) {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	// Only the changed part is replaced: the first two lines are the same.
	if len(edits) != 1 || edits[0].Range.Start.Line != 2 {
		t.Errorf("edits %+v, want one starting on the third line", edits)
	}

	// Already formatted: nothing to do.
	if err := s.didChange(fakeContext(nil), &protocol.DidChangeTextDocumentParams{
		TextDocument:   protocol.VersionedTextDocumentIdentifier{TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri}, Version: 2},
		ContentChanges: []any{protocol.TextDocumentContentChangeEventWhole{Text: string(want)}},
	}); err != nil {
		t.Fatal(err)
	}
	edits, err = s.formatting(fakeContext(nil), &protocol.DocumentFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Options:      protocol.FormattingOptions{"tabSize": float64(4)},
	})
	if err != nil || edits == nil || len(edits) != 0 {
		t.Errorf("formatted code: got %+v, %v", edits, err)
	}

	// A syntax error: left alone, without an error.
	if err := s.didChange(fakeContext(nil), &protocol.DidChangeTextDocumentParams{
		TextDocument:   protocol.VersionedTextDocumentIdentifier{TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri}, Version: 3},
		ContentChanges: []any{protocol.TextDocumentContentChangeEventWhole{Text: "machine M {\n  x =\n"}},
	}); err != nil {
		t.Fatal(err)
	}
	edits, err = s.formatting(fakeContext(nil), &protocol.DocumentFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Options:      protocol.FormattingOptions{"tabSize": float64(4)},
	})
	if err != nil || edits != nil {
		t.Errorf("syntax error: got %+v, %v", edits, err)
	}
}
