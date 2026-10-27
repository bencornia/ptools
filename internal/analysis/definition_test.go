package analysis

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const typesFile = `type tRequest = (source: Client, amount: int);
enum tStatus { SUCCESS, FAILURE }
event eRequest : tRequest;
event eResponse : tStatus;
fun Helper(): int { return 1; }
`

const clientFile = `machine Client {
  var server: Server;
  var amount: int;

  start state Init {
    entry (s: Server) {
      var req: tRequest;
      server = s;
      req = (source = this, amount = amount);
      send server, eRequest, req;
      goto Wait;
    }
  }

  state Wait {
    on eResponse do (status: tStatus) {
      if (status == SUCCESS) { goto Init; }
    }
    on null goto Done with Finish;
  }

  state Done {}

  fun Finish() {
    var amount: int;
    amount = Helper();
    print format("{0}", req.amount);
  }
}

machine Server {
  start state Serve {}
  state Wait {}
}

invariant served: forall (c: Client) :: c is Client;

module Client = { Client };
test tc [main=Client]: (union Client, { Server });

Lemma lemmas {
  invariant waiting: forall (c: Client) :: c is Wait;
}
Proof {
  prove lemmas_waiting;
}
`

const (
	typesURI  = "file:///types.p"
	clientURI = "file:///client.p"
)

// at returns the position of the occurrence-th (1-based) match of needle in
// text, offset by delta characters. Test sources are ASCII, so bytes and
// UTF-16 units agree.
func at(t *testing.T, text, needle string, occurrence, delta int) protocol.Position {
	t.Helper()
	offset := -1
	for i := 0; i < occurrence; i++ {
		next := strings.Index(text[offset+1:], needle)
		if next < 0 {
			t.Fatalf("occurrence %d of %q not found", occurrence, needle)
		}
		offset += next + 1
	}
	offset += delta
	line := strings.Count(text[:offset], "\n")
	col := offset - (strings.LastIndex(text[:offset], "\n") + 1)
	return protocol.Position{Line: protocol.UInteger(line), Character: protocol.UInteger(col)}
}

func TestDefinition(t *testing.T) {
	parser := NewParser()
	t.Cleanup(parser.Close)
	types := Parse(parser, typesURI, 1, typesFile)
	t.Cleanup(types.Close)
	client := Parse(parser, clientURI, 1, clientFile)
	t.Cleanup(client.Close)
	workspace := []*Document{types, client}

	type target struct {
		uri  string
		text string
		// occurrence of text in the target file
		occurrence int
	}
	tests := []struct {
		name string
		// The cursor goes delta characters into the occurrence-th match of
		// cursor in client.p.
		cursor     string
		occurrence int
		delta      int
		want       []target
	}{
		{"machine variable", "server = s", 1, 0, []target{{clientURI, "server", 1}}},
		{"handler parameter", "= s;", 1, 2, []target{{clientURI, "s: Server", 1}}},
		{"handler local", "req = (", 1, 0, []target{{clientURI, "req", 1}}},
		{"local shadows machine variable", "amount = Helper", 1, 0, []target{{clientURI, "amount: int;\n    amount = Helper", 1}}},
		{"machine variable inside tuple value", "= amount)", 1, 2, []target{{clientURI, "amount", 1}}},
		{"type in another file", "tRequest;", 1, 0, []target{{typesURI, "tRequest", 1}}},
		{"event in send", "eRequest, req", 1, 0, []target{{typesURI, "eRequest", 1}}},
		{"event in handler", "eResponse do", 1, 0, []target{{typesURI, "eResponse", 1}}},
		{"enum element", "SUCCESS)", 1, 0, []target{{typesURI, "SUCCESS", 1}}},
		{"goto state", "goto Wait", 1, 5, []target{{clientURI, "Wait", 2}}},
		{"goto state with handler", "goto Done", 1, 5, []target{{clientURI, "Done", 2}}},
		{"handler function", "with Finish", 1, 5, []target{{clientURI, "Finish", 2}}},
		{"global function", "Helper()", 1, 0, []target{{typesURI, "Helper", 1}}},
		{"machine as type", "server: Server", 1, 8, []target{{clientURI, "Server", 3}}},
		{"quantifier variable", "c is", 1, 0, []target{{clientURI, "c: Client", 1}}},
		{"cursor just after identifier", "Helper()", 1, 6, []target{{typesURI, "Helper", 1}}},
		{"declaration resolves to itself", "state Wait", 1, 6, []target{{clientURI, "Wait", 2}}},
		{"module in module expression", "union Client", 1, 6, []target{{clientURI, "Client = {", 1}}},
		{"machine in module", "{ Client }", 1, 2, []target{{clientURI, "Client {", 1}}},
		{"main machine shares a module's name", "main=Client", 1, 5, []target{{clientURI, "Client {", 1}}},
		{"state tested with is uses the instance's machine", "c is Wait", 1, 5, []target{{clientURI, "Wait", 2}}},
		{"invariant in a Lemma is prefixed with its name", "lemmas_waiting", 1, 0, []target{{clientURI, "waiting:", 1}}},
		{"invariant in a Lemma resolves to itself", "waiting:", 1, 0, []target{{clientURI, "waiting:", 1}}},
		{"field of an undeclared variable is unresolved", "req.amount", 1, 4, nil},
		{"named tuple field is unresolved", "source = this", 1, 0, nil},
		{"keyword is unresolved", "goto Init", 1, 0, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos := at(t, clientFile, tt.cursor, tt.occurrence, tt.delta)
			got := Definition(workspace, client, pos)

			want := []protocol.Location{}
			for _, w := range tt.want {
				text := map[string]string{typesURI: typesFile, clientURI: clientFile}[w.uri]
				start := at(t, text, w.text, w.occurrence, 0)
				// The target text starts with the declared name.
				name, _, _ := strings.Cut(strings.Fields(w.text)[0], ":")
				end := start
				end.Character += protocol.UInteger(len(name))
				want = append(want, protocol.Location{URI: w.uri, Range: protocol.Range{Start: start, End: end}})
			}

			if len(got) != len(want) {
				t.Fatalf("got %d locations %+v, want %+v", len(got), got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("location %d = %+v, want %+v", i, got[i], want[i])
				}
			}
		})
	}
}

func TestPointRoundTrip(t *testing.T) {
	doc := parse(t, "// é😀x\nevent e;\n")
	// In UTF-16, "// é" is 4 units and "😀" is 2, so character 6 is just
	// before the 'x'.
	point := doc.Point(protocol.Position{Line: 0, Character: 6})
	if point.Column != uint(len("// é😀")) {
		t.Errorf("column = %d, want %d", point.Column, len("// é😀"))
	}
	if back := doc.Position(point); back.Character != 6 {
		t.Errorf("round trip character = %d, want 6", back.Character)
	}
	// Past the end of a line clamps to the end of that line.
	if point := doc.Point(protocol.Position{Line: 1, Character: 100}); point.Row != 1 || point.Column != uint(len("event e;")) {
		t.Errorf("clamped point = %+v", point)
	}
}
