package analysis

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const fieldTypes = `type tInner = (x: int, y: int);
type tAlias = tInner;
type tReq = (source: machine, inner: tAlias, items: seq[tInner], byKey: map[int, tInner], pair: (tInner, int));
type tLoop = tLoop;
event eReq : tReq;
fun MakeInner(): tInner { var i: tInner; return i; }
fun External(s: int) return (r: tInner);
`

const fieldUses = `machine M {
  var req: tReq;
  var loop: tLoop;

  start state S {
    on eReq do (p: tReq) {
      var n: int;
      var i: tInner;
      n = p.inner.x;
      n = req.items[0].y;
      n = req.byKey[1].x;
      n = MakeInner().y;
      n = (req.inner as tInner).x;
      n = req.pair.0.x;
      req.inner.y = 1;
      req.items[0].x = 2;
      req.pair.0.y = 3;
      n = External(1).x;
      foreach (i in req.items) { n = i.y; }
      n = req.missing;
      n = n.x;
      n = loop.x;
      n = undeclared.x;
      send p.source, eReq, (source = this, inner = req.inner, items = req.items, byKey = req.byKey, pair = req.pair);
    }
  }
}
`

func TestFieldDefinition(t *testing.T) {
	parser := NewParser()
	t.Cleanup(parser.Close)
	types := Parse(parser, typesURI, 1, fieldTypes)
	t.Cleanup(types.Close)
	uses := Parse(parser, clientURI, 1, fieldUses)
	t.Cleanup(uses.Close)
	if diags := SyntaxDiagnostics(uses); len(diags) > 0 {
		t.Fatalf("test source has syntax errors: %+v", diags)
	}
	workspace := []*Document{types, uses}

	tests := []struct {
		name string
		// The cursor goes on the last identifier of cursor in fieldUses.
		cursor string
		// The field declaration expected, as "name:" text in fieldTypes and
		// its occurrence, or "" for none.
		want       string
		occurrence int
	}{
		{"field of a parameter, through an alias", "p.inner", "inner:", 1},
		{"chained field", "p.inner.x", "x:", 1},
		{"sequence element", "req.items[0].y", "y:", 1},
		{"map value", "req.byKey[1].x", "x:", 1},
		{"function return value", "MakeInner().y", "y:", 1},
		{"cast", "(req.inner as tInner).x", "x:", 1},
		{"unnamed tuple element", "req.pair.0.x", "x:", 1},
		{"assignment target", "req.inner.y = 1", "y:", 1},
		{"indexed assignment target", "req.items[0].x = 2", "x:", 1},
		{"tuple element assignment target", "req.pair.0.y = 3", "y:", 1},
		{"foreign function return parameter", "External(1).x", "x:", 1},
		{"foreach item", "i.y", "y:", 1},
		{"unknown field", "req.missing", "", 0},
		{"field of a primitive", "n.x", "", 0},
		{"alias cycle", "loop.x", "", 0},
		{"field of an undeclared variable", "undeclared.x", "", 0},
		{"tuple value field uses the event payload type", "(source", "source:", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The last identifier in the cursor text, skipping any `= 1`.
			text := strings.TrimSpace(strings.Split(tt.cursor, "=")[0])
			end := len(text)
			start := strings.LastIndexAny(text, ".(") + 1
			pos := at(t, fieldUses, tt.cursor, 1, start)
			got := Definition(workspace, uses, pos)

			if tt.want == "" {
				if len(got) != 0 {
					t.Fatalf("%s: got %+v, want no definition", text[start:end], got)
				}
				return
			}
			begin := at(t, fieldTypes, tt.want, tt.occurrence, 0)
			want := protocol.Location{URI: typesURI, Range: protocol.Range{
				Start: begin,
				End:   protocol.Position{Line: begin.Line, Character: begin.Character + protocol.UInteger(len(tt.want)-1)},
			}}
			if len(got) != 1 || got[0] != want {
				t.Fatalf("%s: got %+v, want %+v", text[start:end], got, want)
			}
		})
	}
}

func TestFieldDeclarationResolvesToItself(t *testing.T) {
	doc := parse(t, fieldTypes)
	pos := at(t, fieldTypes, "inner:", 1, 0)
	got := Definition([]*Document{doc}, doc, pos)
	if len(got) != 1 || got[0].Range.Start != pos {
		t.Errorf("got %+v, want the field itself at %+v", got, pos)
	}
}

const verifierSource = `event eVote: (voter: machine, round: int);
event ePing: (src: machine);
eventset eAny = { eVote, ePing };
interface INode() receives eVote;
machine INode { var count: int; start state S {} }
machine Node {
  var voted: bool;
  start state S {}
  fun helper() {}
}
invariant i1: forall (n: Node) :: n.voted;
invariant i2: forall (e: eVote) :: e.round == 0;
invariant i3: forall (e: eAny) :: e.src == e.src;
invariant i4: forall (x: INode) :: x.count == 0;
invariant i5: forall (n: Node) :: n.missing;
invariant i6: forall (n: Node) :: n.helper;
spec Consistency observes eVote { var kv: int; start state S {} }
invariant i7: Consistency.kv == 0;
`

func TestVerifierFieldDefinition(t *testing.T) {
	doc := parse(t, verifierSource)
	if diags := SyntaxDiagnostics(doc); len(diags) > 0 {
		t.Fatalf("test source has syntax errors: %+v", diags)
	}
	tests := []struct {
		name, cursor, want string
	}{
		{"machine variable", "n.voted", "voted:"},
		{"event payload field", "e.round", "round:"},
		{"event set payload field", "e.src", "src:"},
		{"interface uses the machine with its name", "x.count", "count:"},
		{"unknown machine variable", "n.missing", ""},
		{"machine function isn't a field", "n.helper", ""},
		{"spec machine referenced by name", "Consistency.kv", "kv:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos := at(t, verifierSource, tt.cursor, 1, strings.Index(tt.cursor, ".")+1)
			got := Definition([]*Document{doc}, doc, pos)
			if tt.want == "" {
				if len(got) != 0 {
					t.Fatalf("got %+v, want no definition", got)
				}
				return
			}
			if want := at(t, verifierSource, tt.want, 1, 0); len(got) != 1 || got[0].Range.Start != want {
				t.Fatalf("got %+v, want one definition at %+v", got, want)
			}
		})
	}
}

const tupleValueSource = `type tPt = (x: int, y: int);
type tBox = (pt: tPt, n: int);
event ePt : tPt;
event eTwo : (a: int, pt: tPt);
fun TakesPt(k: int, p: tPt) {}
fun MakePt(): tPt { return (x = 1, y = 2); }
machine Target {
  start state Init {
    entry (p: tPt) {}
  }
}
machine Named {
  start state Init { entry Setup; }
  fun Setup(p: tPt) {}
}
machine M {
  var box: tBox;
  var pair: (int, tPt);
  var target: machine;
  var pts: set[tPt];
  var byPt: map[tPt, int];
  var ptSeq: seq[tPt];
  var ptMap: map[int, tPt];
  var count: int;
  start state S {
    entry {
      box = (pt = (x = 10, y = 2), n = 3);
      pair = (1, (x = 11, y = 2));
      TakesPt(1, (x = 12, y = 2));
      send this, ePt, (x = 13, y = 2);
      send this, eTwo, 1, (x = 14, y = 2);
      raise ePt, (x = 15, y = 2);
      announce ePt, (x = 16, y = 2);
      new Target((x = 17, y = 2));
      target = new Target((x = 18, y = 2));
      new Named((x = 19, y = 2));
      goto Next, (x = 20, y = 2);
      send this, ePt, (z = 21, y = 2);
      ptSeq += (0, (x = 24, y = 2));
      ptMap += (1, (x = 25, y = 2));
      byPt += ((x = 26, y = 2), 1);
      pts += ((x = 27, y = 2));
      pts -= ((x = 28, y = 2));
      byPt -= ((x = 29, y = 2));
      if ((x = 30, y = 2) in pts) {}
      byPt[(x = 31, y = 2)] = 1;
      count = byPt[(x = 32, y = 2)];
      if ((x = 33, y = 2) in byPt) {}
    }
  }
  state Next {
    entry (p: tPt) {}
    on ePt do {
      return (x = 22, y = 2);
    }
  }
  fun NotATuple() {
    var n: int;
    n = (x = 23, y = 2);
  }
}
`

func TestTupleValueFieldDefinition(t *testing.T) {
	doc := parse(t, tupleValueSource)
	if diags := SyntaxDiagnostics(doc); len(diags) > 0 {
		t.Fatalf("test source has syntax errors: %+v", diags)
	}
	tests := []struct {
		name string
		// The cursor goes on the first character of cursor.
		cursor, want string
	}{
		{"assignment target", "pt = (", "pt:"},
		{"field of the assignment target", "n = 3", "n:"},
		{"nested tuple value", "x = 10", "x:"},
		{"unnamed tuple element", "x = 11", "x:"},
		{"function argument", "x = 12", "x:"},
		{"function return", "x = 1, y = 2); }", "x:"},
		{"send payload", "x = 13", "x:"},
		{"one of several send payload values", "x = 14", "x:"},
		{"raise payload", "x = 15", "x:"},
		{"announce payload", "x = 16", "x:"},
		{"new statement", "x = 17", "x:"},
		{"new expression", "x = 18", "x:"},
		{"new with a named entry function", "x = 19", "x:"},
		{"goto payload", "x = 20", "x:"},
		{"field not in the type", "z = 21", ""},
		{"return from an event handler", "x = 22", ""},
		{"value assigned to a primitive", "x = 23", ""},
		{"sequence insert", "x = 24", "x:"},
		{"map insert value", "x = 25", "x:"},
		{"map insert key", "x = 26", "x:"},
		{"set add", "x = 27", "x:"},
		{"set remove", "x = 28", "x:"},
		{"map remove", "x = 29", "x:"},
		{"in a set", "x = 30", "x:"},
		{"map index assignment target", "x = 31", "x:"},
		{"map index", "x = 32", "x:"},
		{"in a map", "x = 33", "x:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Definition([]*Document{doc}, doc, at(t, tupleValueSource, tt.cursor, 1, 0))
			if tt.want == "" {
				if len(got) != 0 {
					t.Fatalf("got %+v, want no definition", got)
				}
				return
			}
			if want := at(t, tupleValueSource, tt.want, 1, 0); len(got) != 1 || got[0].Range.Start != want {
				t.Fatalf("got %+v, want one definition at %+v", got, want)
			}
		})
	}
}
