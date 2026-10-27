package analysis

import (
	"fmt"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const clientServer = `
type tRequest = (source: Client, amount: int);
enum tStatus { SUCCESS, FAILURE }
event eRequest : tRequest;
event eResponse;

machine Client {
  var server, other: Server;

  start state Init {
    entry (s: Server) {
      server = s;
      goto Send;
    }
  }

  state Send {
    on eResponse do HandleResponse;
    on null goto Init;
  }

  fun HandleResponse() {
    var count: int;
  }
}

spec Safety observes eRequest {
  start state Watching {
    ignore eRequest;
  }
}

scenario Covered observes eRequest, eResponse {
  start hot state Waiting {
    on eResponse goto Done;
  }
  cold state Done { }
}
`

func parse(t *testing.T, text string) *Document {
	t.Helper()
	parser := NewParser()
	t.Cleanup(parser.Close)
	doc := Parse(parser, "file:///test.p", 1, text)
	t.Cleanup(doc.Close)
	return doc
}

// outline renders symbols one per line, indented by depth, as "name (detail)".
func outline(symbols []protocol.DocumentSymbol) string {
	var b strings.Builder
	var write func([]protocol.DocumentSymbol, int)
	write = func(symbols []protocol.DocumentSymbol, depth int) {
		for _, s := range symbols {
			detail := ""
			if s.Detail != nil {
				detail = " (" + *s.Detail + ")"
			}
			fmt.Fprintf(&b, "%s%s%s\n", strings.Repeat("  ", depth), s.Name, detail)
			write(s.Children, depth+1)
		}
	}
	write(symbols, 0)
	return b.String()
}

func TestValidProgramHasNoDiagnostics(t *testing.T) {
	doc := parse(t, clientServer)
	if diags := SyntaxDiagnostics(doc); len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %+v\ntree: %s", diags, doc.Tree.RootNode().ToSexp())
	}
}

func TestSymbols(t *testing.T) {
	doc := parse(t, clientServer)
	want := `tRequest (type)
tStatus (enum)
  SUCCESS
  FAILURE
eRequest (event)
eResponse (event)
Client (machine)
  server (var)
  other (var)
  Init (state)
    entry
  Send (state)
    on eResponse
    on null
  HandleResponse (fun)
    count (var)
Safety (spec)
  Watching (state)
Covered (scenario)
  Waiting (state)
    on eResponse
  Done (state)
`
	if got := outline(Symbols(doc)); got != want {
		t.Errorf("outline mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestSymbolSelectionRangeIsName(t *testing.T) {
	doc := parse(t, "machine Client {}\n")
	symbols := Symbols(doc)
	if len(symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(symbols))
	}
	want := protocol.Range{
		Start: protocol.Position{Line: 0, Character: 8},
		End:   protocol.Position{Line: 0, Character: 14},
	}
	if symbols[0].SelectionRange != want {
		t.Errorf("selection range = %+v, want %+v", symbols[0].SelectionRange, want)
	}
}

func TestMissingTokenDiagnostic(t *testing.T) {
	doc := parse(t, "fun f() {\n  x = 1\n}\n")
	diags := SyntaxDiagnostics(doc)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %+v\ntree: %s", diags, doc.Tree.RootNode().ToSexp())
	}
	want := protocol.Position{Line: 1, Character: 7}
	if diags[0].Range.Start != want || diags[0].Message != "expected `;`" {
		t.Errorf("got %q at %+v, want \"expected `;`\" at %+v", diags[0].Message, diags[0].Range.Start, want)
	}
}

func TestErrorKeepsRestOfOutline(t *testing.T) {
	doc := parse(t, "event eA;\nmachine M { state S { on } }\nevent eB;\n")
	if len(SyntaxDiagnostics(doc)) == 0 {
		t.Fatalf("expected a syntax error\ntree: %s", doc.Tree.RootNode().ToSexp())
	}
	got := outline(Symbols(doc))
	for _, name := range []string{"eA", "eB", "M"} {
		if !strings.Contains(got, name) {
			t.Errorf("outline missing %q:\n%s", name, got)
		}
	}
}

func TestPositionCountsUTF16(t *testing.T) {
	// "é" is 2 bytes in UTF-8 and 1 unit in UTF-16; "😀" is 4 bytes and 2 units.
	doc := parse(t, "// é😀\nevent e;\n")
	comment := doc.Tree.RootNode().NamedChild(0)
	end := doc.Position(comment.EndPosition())
	if end.Line != 0 || end.Character != 6 {
		t.Errorf("comment end = %+v, want line 0 character 6", end)
	}
}

func TestCallStatementNeedsSemicolon(t *testing.T) {
	doc := parse(t, "fun f() {\n  g(1)\n}\n")
	diags := SyntaxDiagnostics(doc)
	want := protocol.Position{Line: 1, Character: 6}
	if len(diags) != 1 || diags[0].Range.Start != want || diags[0].Message != "expected `;`" {
		t.Errorf("got %+v, want \"expected `;`\" at %+v", diags, want)
	}
	if diags := SyntaxDiagnostics(parse(t, "fun f() {\n  g(1);\n}\n")); len(diags) != 0 {
		t.Errorf("with the semicolon, got %+v", diags)
	}
}
