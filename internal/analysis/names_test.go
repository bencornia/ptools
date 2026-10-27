package analysis

import (
	"strings"
	"testing"
)

const namesTypes = `type tReq = (amount: int, source: machine);
event ePing;
event eRing;
fun Helper(): int { return 1; }
`

func TestUndefinedNameFixes(t *testing.T) {
	tests := []struct {
		name, body string
		message    string
		// titles are the fixes offered, in order.
		titles []string
	}{
		{"misspelled local", "count = 1;", "undefined name `count`", []string{"Change to `counts`"}},
		{"misspelled function: only functions", "Helpr();", "undefined name `Helpr`", []string{"Change to `Helper`"}},
		{"misspelled type: only types", "var r: tReqq;", "undefined name `tReqq`", []string{"Change to `tReq`"}},
		{"misspelled state", "goto Dnoe;", "undefined state `Dnoe`", []string{"Change to `Done`"}},
		{"misspelled field", "counts = req.amout;", "no field `amout` in `tReq`", []string{"Change to `amount`"}},
		{"misspelled field in a tuple value", "req = (amout = 1, source = this);", "no field `amout` in `tReq`", []string{"Change to `amount`"}},
		{"nothing close", "zzzzzz = 1;", "undefined name `zzzzzz`", nil},
		{"case first", "raise eping;", "undefined event `eping`", []string{"Declare `event eping;`", "Change to `ePing`", "Change to `eRing`"}},
		{"field of a type that isn't known", "counts = Helper().whatever;", "", nil},
		{"defined names", "counts = Helper() + req.amount; goto Done;", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := "machine M {\n  var counts: int;\n  var req: tReq;\n  var Helpers, tReqs: int;\n  start state S {\n    entry {\n      " + tt.body + "\n    }\n  }\n  state Done {}\n}\n"
			doc := parse(t, text)
			if diags := SyntaxDiagnostics(doc); len(diags) > 0 {
				t.Fatalf("syntax errors: %+v", diags)
			}
			workspace := []*Document{parse(t, namesTypes), doc}
			diags := Diagnostics(workspace, doc)
			if tt.message == "" {
				if len(diags) != 0 {
					t.Errorf("got %+v", diags)
				}
				return
			}
			if len(diags) != 1 || diags[0].Message != tt.message {
				t.Fatalf("diagnostics %+v, want just %q", diags, tt.message)
			}
			titles := []string{}
			for _, f := range Fixes(workspace, doc) {
				titles = append(titles, f.Title)
				if f.Diagnostic.Message != tt.message {
					t.Errorf("fix %q is for %q", f.Title, f.Diagnostic.Message)
				}
			}
			if strings.Join(titles, " | ") != strings.Join(tt.titles, " | ") {
				t.Errorf("fixes %q, want %q", titles, tt.titles)
			}
		})
	}
}

func TestEditDistance(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want int
	}{
		{"amount", "amount", 0},
		{"amout", "amount", 1},
		{"tRqe", "tReq", 1},
		{"Dnoe", "Done", 1},
		{"abc", "xyz", 3},
		{"", "ab", 2},
	} {
		if got := editDistance(tt.a, tt.b); got != tt.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
