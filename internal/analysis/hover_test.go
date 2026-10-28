package analysis

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const hoverTypes = `// A request from a client.
// Second line.
type tReq = (source: machine, amount: int);

/* Payload-free ping. */
event ePing;

enum tColor { RED, GREEN }

// Not attached: a blank line follows.

event eLonely; // <.>
event eCallout;
`

const hoverMain = `machine Client {
  // The server this client talks to.
  var server, backup: machine;

  start hot state Init {
    entry (s: machine) {
      var req: tReq;
      req = (source = s, amount = 1);
      send server, ePing;
      send server, eLonely;
      send server, eCallout;
      goto Done;
    }
  }

  state Done {
    on ePing do Inc;
  }

  /** Adds one.
   *  Really.
   */
  fun Inc(x: int): int {
    return x + 1;
  }
}

Lemma lemmas {
  invariant waiting: forall (c: Client) :: c is Done;
}
Proof {
  prove lemmas_waiting;
}

param color: tColor;
invariant usesRed: color == RED;
`

func TestHover(t *testing.T) {
	parser := NewParser()
	t.Cleanup(parser.Close)
	types := Parse(parser, typesURI, 1, hoverTypes)
	t.Cleanup(types.Close)
	main := Parse(parser, clientURI, 1, hoverMain)
	t.Cleanup(main.Close)
	for _, d := range []*Document{types, main} {
		if diags := SyntaxDiagnostics(d); len(diags) > 0 {
			t.Fatalf("test source has syntax errors: %+v", diags)
		}
	}
	workspace := []*Document{types, main}

	code := func(s string) string { return "```p\n" + s + "\n```" }
	tests := []struct {
		name string
		// The cursor goes delta characters into the first match of cursor
		// in hoverMain.
		cursor string
		delta  int
		want   string
	}{
		{"type alias in another file, with a multi-line comment", "tReq;", 0,
			code("type tReq = (source: machine, amount: int);") + "\n\nA request from a client.\nSecond line.\n\nDeclared in `types.p`, line 3"},
		{"one of several variables declared together", "server, ePing", 5,
			code("var server: machine") + "\n\nThe server this client talks to."},
		{"the other variable, from its declaration", "backup", 0,
			code("var backup: machine") + "\n\nThe server this client talks to."},
		{"state header without its body", "goto Done", 5, code("state Done")},
		{"state with modifiers", "Init", 0, code("start hot state Init")},
		{"function signature with a block comment", "Inc(x", 0, code("fun Inc(x: int): int") + "\n\nAdds one.\nReally."},
		{"handler function", "do Inc", 3, code("fun Inc(x: int): int") + "\n\nAdds one.\nReally."},
		{"parameter", "x + 1", 0, code("x: int")},
		{"local variable has no doc comment", "req = (", 0, code("var req: tReq")},
		{"tuple value field", "amount = 1", 0, code("amount: int") + "\n\nDeclared in `types.p`, line 3"},
		{"event with a /* */ comment", "ePing;", 0, code("event ePing;") + "\n\nPayload-free ping.\n\nDeclared in `types.p`, line 6"},
		{"comment separated by a blank line", "eLonely", 0, code("event eLonely;") + "\n\nDeclared in `types.p`, line 12"},
		{"a callout on the line above isn't a doc comment", "eCallout", 0, code("event eCallout;") + "\n\nDeclared in `types.p`, line 13"},
		{"enum element", "RED;", 0, code("enum tColor { RED, GREEN }") + "\n\nDeclared in `types.p`, line 8"},
		{"global parameter", "color ==", 0, code("param color: tColor")},
		{"machine", "c: Client", 3, code("machine Client")},
		{"invariant in a Lemma", "lemmas_waiting", 0, code("lemmas_waiting: forall (c: Client) :: c is Done;")},
		{"nothing to hover", "goto Done", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos := at(t, hoverMain, tt.cursor, 1, tt.delta)
			hover := Hover(workspace, main, pos)
			if tt.want == "" {
				if hover != nil {
					t.Fatalf("got %+v, want no hover", hover)
				}
				return
			}
			if hover == nil {
				t.Fatal("got no hover")
			}
			got := hover.Contents.(protocol.MarkupContent)
			if got.Kind != protocol.MarkupKindMarkdown || got.Value != tt.want {
				t.Errorf("hover\n got %q\nwant %q", got.Value, tt.want)
			}
			if hover.Range == nil || hover.Range.Start.Line != pos.Line {
				t.Errorf("range %+v doesn't cover the cursor at %+v", hover.Range, pos)
			}
		})
	}
}

func TestHoverClipsLongDeclarations(t *testing.T) {
	long := "type tLong = (\n" + strings.Repeat("  f: int,\n", 30) + "  last: int);\nevent e: tLong;\n"
	doc := parse(t, long)
	hover := Hover([]*Document{doc}, doc, at(t, long, "tLong;", 1, 0))
	if hover == nil {
		t.Fatal("got no hover")
	}
	value := hover.Contents.(protocol.MarkupContent).Value
	if lines := strings.Count(value, "\n"); lines > maxSignatureLines+2 || !strings.Contains(value, " …") {
		t.Errorf("long declaration not clipped:\n%s", value)
	}
}

func TestHoverAmbiguousNameShowsEach(t *testing.T) {
	src := "event eDup;\nevent eDup: int;\nmachine M { start state S { on eDup do {} } }\n"
	doc := parse(t, src)
	hover := Hover([]*Document{doc}, doc, at(t, src, "eDup do", 1, 0))
	if hover == nil {
		t.Fatal("got no hover")
	}
	want := "```p\nevent eDup;\n```\n\n---\n\n```p\nevent eDup: int;\n```"
	if got := hover.Contents.(protocol.MarkupContent).Value; got != want {
		t.Errorf("hover\n got %q\nwant %q", got, want)
	}
}
