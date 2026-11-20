package analysis

import (
	"slices"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const renameTypes = `type tReq = (source: machine, amount: int);
event eReq : tReq;
fun Helper(): int { return 1; }
fun AsReq(): any { return (source = null, amount = 2) as tReq; }
`

const renameMain = `machine Client {
  var count: int;
  var other: int;

  start state Init {
    entry {
      var local: int;
      count = count + 1;
      local = count;
      send this, eReq, (source = this, amount = count);
      goto Next;
    }
  }

  state Next {}

  fun UsesLocal() {
    var n: int;
    n = count;
  }

  fun UsesHelper() {
    count = Helper();
  }
}

machine Server {
  start state Next {}
}

Lemma lemmas {
  invariant waiting: forall (c: Client) :: c is Next;
}
Proof {
  prove lemmas_waiting using lemmas;
}
`

// applyEdits applies LSP edits to ASCII text.
func applyEdits(t *testing.T, text string, edits []protocol.TextEdit) string {
	t.Helper()
	offset := func(p protocol.Position) int {
		lines := strings.SplitAfter(text, "\n")
		o := 0
		for _, l := range lines[:p.Line] {
			o += len(l)
		}
		return o + int(p.Character)
	}
	sorted := slices.Clone(edits)
	slices.SortFunc(sorted, func(a, b protocol.TextEdit) int { return offset(b.Range.Start) - offset(a.Range.Start) })
	for _, e := range sorted {
		text = text[:offset(e.Range.Start)] + e.NewText + text[offset(e.Range.End):]
	}
	return text
}

func TestRename(t *testing.T) {
	parser := NewParser()
	t.Cleanup(parser.Close)
	types := Parse(parser, typesURI, 1, renameTypes)
	t.Cleanup(types.Close)
	main := Parse(parser, clientURI, 1, renameMain)
	t.Cleanup(main.Close)
	for _, d := range []*Document{types, main} {
		if diags := SyntaxDiagnostics(d); len(diags) > 0 {
			t.Fatalf("test source has syntax errors: %+v", diags)
		}
	}
	docs := []*Document{main, types}
	workspaceOf := func(*Document) []*Document { return docs }

	tests := []struct {
		name string
		// The cursor goes delta characters into the first match of cursor
		// in renameMain, or in renameTypes if inTypes.
		cursor  string
		delta   int
		inTypes bool
		newName string
		// The expected text of each file, or an expected error.
		wantMain, wantTypes string
		wantErr             string
	}{
		{
			name: "machine variable", cursor: "count: int", newName: "total",
			wantMain: strings.ReplaceAll(renameMain, "count", "total"),
		},
		{
			name: "state, not another machine's state of the same name", cursor: "goto Next", delta: 5, newName: "Done",
			wantMain: strings.NewReplacer("goto Next", "goto Done", "  state Next {}", "  state Done {}", "c is Next", "c is Done").Replace(renameMain),
		},
		{
			name: "event, from a use in another file", cursor: "eReq,", newName: "eRequest",
			wantMain:  strings.ReplaceAll(renameMain, "eReq", "eRequest"),
			wantTypes: strings.ReplaceAll(renameTypes, "eReq", "eRequest"),
		},
		{
			name: "tuple field, including a tuple value's field", cursor: "amount = count", newName: "total",
			wantMain:  strings.ReplaceAll(renameMain, "amount = count", "total = count"),
			wantTypes: strings.NewReplacer("amount: int", "total: int", "amount = 2", "total = 2").Replace(renameTypes),
		},
		{
			name: "tuple field, from a tuple value cast to its type", cursor: "amount = 2", inTypes: true, newName: "total",
			wantMain:  strings.ReplaceAll(renameMain, "amount = count", "total = count"),
			wantTypes: strings.NewReplacer("amount: int", "total: int", "amount = 2", "total = 2").Replace(renameTypes),
		},
		{
			name: "invariant in a Lemma, from its declaration", cursor: "waiting:", newName: "ready",
			wantMain: strings.NewReplacer("waiting:", "ready:", "lemmas_waiting", "lemmas_ready").Replace(renameMain),
		},
		{
			name: "invariant in a Lemma, from a reference", cursor: "lemmas_waiting", newName: "lemmas_ready",
			wantMain: strings.NewReplacer("waiting:", "ready:", "lemmas_waiting", "lemmas_ready").Replace(renameMain),
		},
		{
			name: "Lemma, and its invariants' references", cursor: "Lemma lemmas", delta: 6, newName: "facts",
			wantMain: strings.NewReplacer("Lemma lemmas", "Lemma facts", "using lemmas", "using facts", "lemmas_waiting", "facts_waiting").Replace(renameMain),
		},
		{
			name: "same name", cursor: "count: int", newName: "count",
			wantMain: renameMain,
		},

		{name: "local would capture a machine variable's use", cursor: "local: int", newName: "count", wantErr: "would make the count on line 8 refer to this instead"},
		{name: "use would be captured by another function's local", cursor: "count: int", newName: "n", wantErr: "reference on line 19 of client.p refer to a different n"},
		{name: "machine variable would capture a global function's use", cursor: "count: int", newName: "Helper", wantErr: "would make the Helper on line 23 refer to this instead"},
		{name: "already declared in the same scope", cursor: "count: int", newName: "other", wantErr: "other is already declared on line 3 of client.p"},
		{name: "already declared globally", cursor: "eReq,", newName: "Helper", wantErr: "Helper is already declared on line 3 of types.p"},
		{name: "duplicate tuple field", cursor: "amount: int", inTypes: true, newName: "source", wantErr: "already has a field named source"},
		{name: "grouped invariant renamed without its prefix", cursor: "lemmas_waiting", newName: "ready", wantErr: `must start with "lemmas_"`},
		{name: "not a name", cursor: "count: int", newName: "2x", wantErr: "isn't a valid P name"},
		{name: "keyword", cursor: "count: int", newName: "state", wantErr: "is a P keyword"},
		{name: "nothing to rename", cursor: "goto Next", newName: "x", wantErr: "no name here"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, text := main, renameMain
			if tt.inTypes {
				doc, text = types, renameTypes
			}
			edits, err := Rename(docs, workspaceOf, doc, at(t, text, tt.cursor, 1, tt.delta), tt.newName)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got error %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantTypes := tt.wantTypes
			if wantTypes == "" {
				wantTypes = renameTypes
			}
			if got := applyEdits(t, renameMain, edits[clientURI]); got != tt.wantMain {
				t.Errorf("client.p after rename:\n%s\nwant:\n%s", got, tt.wantMain)
			}
			if got := applyEdits(t, renameTypes, edits[typesURI]); got != wantTypes {
				t.Errorf("types.p after rename:\n%s\nwant:\n%s", got, wantTypes)
			}
		})
	}
}

func TestPrepareRename(t *testing.T) {
	doc := parse(t, renameTypes+renameMain)
	text := renameTypes + renameMain
	docs := []*Document{doc}

	pos := at(t, text, "count = count", 1, 0)
	if rng := PrepareRename(docs, doc, pos); rng == nil || rng.Start != pos || rng.End.Character != pos.Character+5 {
		t.Errorf("PrepareRename on count = %+v, want its range at %+v", rng, pos)
	}
	if rng := PrepareRename(docs, doc, at(t, text, "goto", 1, 1)); rng != nil {
		t.Errorf("PrepareRename on a keyword = %+v, want nil", rng)
	}
}
