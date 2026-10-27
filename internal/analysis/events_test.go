package analysis

import (
	"strings"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// undefinedEventFixes returns the fixes undefinedEvent offers in doc.
func undefinedEventFixes(workspace []*Document, doc *Document) []Fix {
	fixes := []Fix{}
	doc.walk(func(n *sitter.Node) bool {
		fixes = append(fixes, undefinedEvent(workspace, doc, n)...)
		return true
	})
	return fixes
}

func TestUndefinedEventFixes(t *testing.T) {
	const machine = `machine M {
  var n: int;
  var req: tReq;
  start state S {
    BODY
  }
}
`
	types := "type tReq = (amount: int);\n"
	tests := []struct {
		name, body, declaration string
	}{
		{"handler parameter", "on eNew do (r: tReq) {}", "event eNew: tReq;"},
		{"goto handler parameter", "on eNew goto S with (x: map[int, bool]) {}", "event eNew: map[int, bool];"},
		{"handler without a parameter", "on eNew do {}", "event eNew;"},
		{"defer", "defer eNew;", "event eNew;"},
		{"send with a literal", "entry { send this, eNew, 1; }", "event eNew: int;"},
		{"send with variables in a named tuple", "entry { send this, eNew, (count = n, r = req, who = this); }",
			"event eNew: (count: int, r: tReq, who: machine);"},
		{"send with several values", "entry { send this, eNew, n, true; }", "event eNew: (int, bool);"},
		{"send with something unknown", "entry { send this, eNew, F(); }", "event eNew: any;"},
		{"raise without a payload", "entry { raise eNew; }", "event eNew;"},
		{"announce with a string", "entry { announce eNew, \"hi\"; }", "event eNew: string;"},
		{"receive case", "entry { receive { case eNew: (b: bool) {} } }", "event eNew: bool;"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := "// Events.\nevent eOld;\n\n" + strings.Replace(machine, "BODY", tt.body, 1)
			doc := parse(t, text)
			if diags := SyntaxDiagnostics(doc); len(diags) > 0 {
				t.Fatalf("syntax errors: %+v", diags)
			}
			workspace := []*Document{parse(t, types), doc}
			fixes := undefinedEventFixes(workspace, doc)
			if len(fixes) != 1 {
				t.Fatalf("got %d fixes, want 1", len(fixes))
			}
			f := fixes[0]
			if f.Diagnostic.Message != "undefined event `eNew`" {
				t.Errorf("message %q", f.Diagnostic.Message)
			}
			if f.Title != "Declare `"+tt.declaration+"`" {
				t.Errorf("title %q, want it to declare %q", f.Title, tt.declaration)
			}
			// It goes after the other event.
			fixed := applyEdits(t, text, f.Edits)
			if want := "event eOld;\n" + tt.declaration + "\n"; !strings.Contains(fixed, want) {
				t.Errorf("after the fix:\n%s", fixed)
			}
			fixedDoc := parse(t, fixed)
			if len(undefinedEventFixes([]*Document{parse(t, types), fixedDoc}, fixedDoc)) != 0 || len(SyntaxDiagnostics(fixedDoc)) != 0 {
				t.Errorf("still has problems after the fix:\n%s", fixed)
			}
		})
	}
}

func TestDeclaredEventsAreNotReported(t *testing.T) {
	events := parse(t, "event eA;\neventset eAll = { eA };\n")
	doc := parse(t, `machine M {
  start state S {
    entry (e: event) { send this, e; raise eA; announce eAll; }
    on eA, halt, null do {}
    ignore eAll;
  }
}
spec W observes eA { start state S {} }
`)
	if fixes := undefinedEventFixes([]*Document{events, doc}, doc); len(fixes) != 0 {
		t.Errorf("got %+v", fixes[0].Diagnostic)
	}
}

func TestDeclarationGoesAboveTheFirstDeclarationAndItsComments(t *testing.T) {
	text := "// The machine.\n// Second line.\nmachine M {\n  start state S { entry { raise eNew; } }\n}\n"
	doc := parse(t, text)
	fixes := undefinedEventFixes([]*Document{doc}, doc)
	if len(fixes) != 1 {
		t.Fatalf("got %d fixes", len(fixes))
	}
	want := "event eNew;\n\n// The machine.\n// Second line.\nmachine M {"
	if got := applyEdits(t, text, fixes[0].Edits); !strings.HasPrefix(got, want) {
		t.Errorf("got\n%s", got)
	}
}
