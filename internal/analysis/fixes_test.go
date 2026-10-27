package analysis

import (
	"strings"
	"testing"
)

const fixesTypes = `type tOne = (int);
type tNamed = (amount: int);
type tPair = (int, int);
event eOne: (int);
event eNamed: tNamed;
`

func TestFixes(t *testing.T) {
	tests := []struct {
		name string
		// body goes in a function in a machine; vars are declared above it.
		body string
		// message is part of the diagnostic, and fixed the body after the
		// fix; both are empty when there's nothing to fix.
		message, fixed string
	}{
		{"one-field named tuple without a comma", "send m, eNamed, (amount = 1);",
			"a tuple with one field needs a trailing comma: `(amount = 1,)`", "send m, eNamed, (amount = 1,);"},
		{"named tuple with its comma", "send m, eNamed, (amount = 1,);", "", ""},
		{"value in parentheses for an unnamed tuple", "one = (1);",
			"this is `1` in parentheses, not a tuple", "one = (1,);"},
		{"value in parentheses for a named tuple", "named = (n + 1);",
			"this is `n + 1` in parentheses, not a tuple", "named = (amount = n + 1,);"},
		{"variable in parentheses as an event payload", "send m, eOne, (n);",
			"this is `n` in parentheses, not a tuple", "send m, eOne, (n,);"},
		{"argument for a tuple parameter", "Take((n));",
			"this is `n` in parentheses, not a tuple", "Take((n,));"},
		{"value in parentheses where it isn't a tuple", "n = (n + 1);", "", ""},
		{"tuple in parentheses", "one = (one);", "", ""},
		{"value of unknown type", "one = (a);", "", ""},
		{"two-element tuple expected", "pair = (1);", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := "machine M {\n  var m: machine;\n  var n: int;\n  var a: any;\n  var one: tOne;\n  var named: tNamed;\n  var pair: tPair;\n" +
				"  fun Take(x: (int)) {}\n  fun F() {\n    " + tt.body + "\n  }\n}\n"
			types := parse(t, fixesTypes)
			if diags := SyntaxDiagnostics(types); len(diags) > 0 {
				t.Fatalf("test types have syntax errors: %+v", diags)
			}
			doc := parse(t, text)
			workspace := []*Document{types, doc}
			fixes := Fixes(workspace, doc)
			if tt.message == "" {
				if len(fixes) != 0 {
					t.Errorf("got fix %q for %q", fixes[0].Diagnostic.Message, tt.body)
				}
				return
			}
			if len(fixes) != 1 {
				t.Fatalf("got %d fixes, want 1", len(fixes))
			}
			f := fixes[0]
			if !strings.Contains(f.Diagnostic.Message, tt.message) {
				t.Errorf("message %q, want it to contain %q", f.Diagnostic.Message, tt.message)
			}
			if got := applyEdits(t, text, f.Edits); !strings.Contains(got, "    "+tt.fixed+"\n") {
				t.Errorf("after the fix:\n%s\nwant the line %q", got, tt.fixed)
			}

			// Every fix's problem is reported, once.
			count := 0
			for _, d := range Diagnostics(workspace, doc) {
				if d.Message == f.Diagnostic.Message {
					count++
				}
			}
			if count != 1 {
				t.Errorf("the problem is reported %d times, want once", count)
			}
		})
	}
}
