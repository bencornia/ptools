package analysis

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/bencornia/p-lsp/internal/style"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

const checksSource = `type Unused = int;
type Used = int;
enum Color { RED, GREEN }
enum Shade { DARK }
event Ping: Used;
event Pong;
event Stray;
eventset Spare = { Ping };
param given: int;
param ignored: int;

fun helper(): Color { var spare: int; var kept: int; kept = given; return RED; }
fun forgotten() { }

machine Server {
  var count: int;
  var idle: int;
  start state Init {
    entry {
      var count: int;
      raise Ping, 1;
      count = 1;
    }
    on Ping do (count: Used) {
      if (count > 0) { goto Serving; } else { return; }
      print "never";
    }
    on Pong goto Init;
  }
  state Serving { on null goto Done; }
  state Done { }
  state Lonely { on null goto Orphan; }
  state Orphan { }
  fun loop() { while (true) { break; count = count + 1; } }
  fun reset() { goto Init; }
}

spec Watcher observes Ping, Pong {
  start state Watching { }
}
`

func TestChecks(t *testing.T) {
	doc := parse(t, checksSource)
	got := checkMessages(Checks([]*Document{doc}, doc, style.Default().Checks))
	want := []string{
		"1:6 unused: type `Unused` is never used",
		"4:6 unused: type `Shade` is never used",
		"6:7 never-sent: event `Pong` is never sent, raised or announced",
		"7:7 unused: event `Stray` is never used",
		"8:10 unused: event set `Spare` is never used",
		"10:7 unused: parameter `ignored` is never used",
		"12:5 unused: function `helper` is never used",
		"12:27 unused: variable `spare` is never used",
		"13:5 unused: function `forgotten` is never used",
		"17:7 unused: variable `idle` is never used",
		"20:11 shadowing: `count` hides the machine variable `count`",
		"22:7 unreachable-code: unreachable code after `raise`",
		"24:17 shadowing: `count` hides the machine variable `count`",
		"26:7 unreachable-code: unreachable code: every path through the statement before it leaves",
		"32:9 unreachable-state: state `Lonely` can't be reached from the start state `Init`",
		"33:9 unreachable-state: state `Orphan` can't be reached from the start state `Init`",
		"34:7 unused: function `loop` is never used",
		"34:38 unreachable-code: unreachable code after `break`",
		"35:7 unused: function `reset` is never used",
		"38:29 never-sent: `Watcher` observes `Pong`, which is never sent, raised or announced",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestChecksAcrossWorkspace(t *testing.T) {
	events := parse(t, "event Ping;\ntype Shared = int;\n")
	main := parse(t, "machine M { var s: Shared; start state S { entry { send this, Ping; s = 1; } } }\n")
	if got := checkMessages(Checks([]*Document{events, main}, events, style.Default().Checks)); len(got) > 0 {
		t.Errorf("names used in another file reported: %v", got)
	}
	if got := checkMessages(Checks(nil, events, style.Default().Checks)); len(got) != 2 {
		t.Errorf("alone, got %v", got)
	}
}

func TestChecksOrder(t *testing.T) {
	doc := parse(t, `event Go;
machine M {
  state Waiting {
    exit { }
    on Go goto Running;
    entry { send this, Go; }
  }
  start state Running {
    entry setup;
    exit { goto Waiting; }
  }
  fun setup() { }
}
spec S observes Go {
  state Late { exit done; entry done; }
  start state Early { on Go goto Late; }
  fun done() { }
}
`)
	checks := style.Checks{Order: style.LevelWarning}
	got := checkMessages(Checks([]*Document{doc}, doc, checks))
	want := []string{
		"6:5 order: `entry` should come before `exit`, on line 4",
		"8:15 order: the start state `Running` should come first, before `Waiting`",
		"15:27 order: `entry` should come before `exit`, on line 15",
		"16:15 order: the start state `Early` should come first, before `Late`",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestOrderFixes(t *testing.T) {
	for _, tc := range []struct{ name, src, want, title string }{{
		name: "start state on its own lines, with its comment",
		src: `machine M {
  var x: int;
  state Waiting {
    on null goto Running;
  }
  // Where it begins.
  start state Running {
    entry { goto Waiting; }
  }
}
`,
		want: `machine M {
  var x: int;
  // Where it begins.
  start state Running {
    entry { goto Waiting; }
  }
  state Waiting {
    on null goto Running;
  }
}
`,
		title: "Move `Running` before `Waiting`",
	}, {
		name: "entry on its own lines, with a comment after it",
		src: `machine M {
  start state S {
    exit { }
    ignore null;
    entry { } // hello
  }
}
`,
		want: `machine M {
  start state S {
    entry { } // hello
    exit { }
    ignore null;
  }
}
`,
		title: "Move `entry` before `exit`",
	}, {
		name:  "on one line",
		src:   "machine M { start state S { exit f; entry f; } fun f() { } }\n",
		want:  "machine M { start state S { entry f; exit f; } fun f() { } }\n",
		title: "Move `entry` before `exit`",
	}, {
		name:  "onto another's line",
		src:   "machine M {\n  start state S { exit f;\n    entry f;\n  }\n  fun f() { }\n}\n",
		want:  "machine M {\n  start state S { entry f; exit f;\n  }\n  fun f() { }\n}\n",
		title: "Move `entry` before `exit`",
	}, {
		name:  "entry after a handler",
		src:   "machine M {\n  start state S {\n    on e goto S;\n    defer d;\n\n    entry {\n    }\n  }\n}\n",
		want:  "machine M {\n  start state S {\n    entry {\n    }\n    on e goto S;\n    defer d;\n  }\n}\n",
		title: "Move `entry` before the `on` on line 3",
	}, {
		name:  "exit after an ignore",
		src:   "machine M { start state S { entry f; ignore i; exit f; } fun f() { } }\n",
		want:  "machine M { start state S { entry f; exit f; ignore i; } fun f() { } }\n",
		title: "Move `exit` before the `ignore` on line 1",
	}, {
		name:  "start state on one line",
		src:   "machine M { state A { } start state B { goto A; } }\n",
		want:  "machine M { start state B { goto A; } state A { } }\n",
		title: "Move `B` before `A`",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			doc := parse(t, tc.src)
			fixes := CheckFixes(nil, doc, style.Checks{Order: style.LevelWarning})
			if len(fixes) != 1 {
				t.Fatalf("got %d fixes", len(fixes))
			}
			if fixes[0].Title != tc.title {
				t.Errorf("title %q", fixes[0].Title)
			}
			want := Checks([]*Document{doc}, doc, style.Checks{Order: style.LevelWarning})
			if len(want) != 1 || fixes[0].Diagnostic.Message != want[0].Message || fixes[0].Diagnostic.Range != want[0].Range {
				t.Errorf("diagnostic %v, Checks reports %v", fixes[0].Diagnostic, want)
			}
			if got := applyEdits(t, tc.src, fixes[0].Edits); got != tc.want {
				t.Errorf("got\n%s\nwant\n%s", got, tc.want)
			}
			fixed := parse(t, applyEdits(t, tc.src, fixes[0].Edits))
			if again := CheckFixes(nil, fixed, style.Checks{Order: style.LevelWarning}); len(again) != 0 {
				t.Errorf("still out of order: %v", again[0].Diagnostic.Message)
			}
		})
	}
	if fixes := CheckFixes(nil, parse(t, "machine M { state A { } start state B { } }\n"), style.Checks{}); len(fixes) != 0 {
		t.Errorf("fixes with the check off: %v", fixes)
	}
}

func TestRemovalFixes(t *testing.T) {
	for _, tc := range []struct{ src, title, want string }{{
		src:   "// Not needed.\ntype Unused = int;\nevent E;\nmachine M { start state S { entry { send this, E; } } }\n",
		title: "Remove the unused type `Unused`",
		want:  "event E;\nmachine M { start state S { entry { send this, E; } } }\n",
	}, {
		src:   "machine M {\n  var a, b: int;\n  start state S { entry { a = 1; } }\n}\n",
		title: "Remove the unused variable `b`",
		want:  "machine M {\n  var a: int;\n  start state S { entry { a = 1; } }\n}\n",
	}, {
		src:   "machine M {\n  var a, b: int;\n  start state S { entry { b = 1; } }\n}\n",
		title: "Remove the unused variable `a`",
		want:  "machine M {\n  var b: int;\n  start state S { entry { b = 1; } }\n}\n",
	}, {
		src:   "machine M {\n  start state S { }\n\n  fun helper() {\n    print \"x\";\n  }\n}\n",
		title: "Remove the unused function `helper`",
		want:  "machine M {\n  start state S { }\n}\n",
	}, {
		src:   "fun f() { var spare: int; print \"x\"; }\n",
		title: "Remove the unused variable `spare`",
		want:  "fun f() { print \"x\"; }\n",
	}, {
		src:   "param a, b: int;\nfun f(): int { return a; }\n",
		title: "Remove the unused parameter `b`",
		want:  "param a: int;\nfun f(): int { return a; }\n",
	}, {
		src:   "machine M {\n  start state S {\n    entry {\n      return;\n      // gone\n      print \"a\";\n      print \"b\";\n    }\n  }\n}\n",
		title: "Remove the unreachable code",
		want:  "machine M {\n  start state S {\n    entry {\n      return;\n    }\n  }\n}\n",
	}, {
		src:   "machine M { start state S { entry { goto S; print \"a\"; } } }\n",
		title: "Remove the unreachable code",
		want:  "machine M { start state S { entry { goto S; } } }\n",
	}, {
		src:   "machine M {\n  start state S { }\n  state Lonely {\n    on null goto S;\n  }\n}\n",
		title: "Remove the state `Lonely`",
		want:  "machine M {\n  start state S { }\n}\n",
	}} {
		doc := parse(t, tc.src)
		fixes := CheckFixes(nil, doc, style.Default().Checks)
		i := slices.IndexFunc(fixes, func(f Fix) bool { return f.Title == tc.title })
		if i < 0 {
			var titles []string
			for _, f := range fixes {
				titles = append(titles, f.Title)
			}
			t.Errorf("no fix %q in %q", tc.title, titles)
			continue
		}
		if got := applyEdits(t, tc.src, fixes[i].Edits); got != tc.want {
			t.Errorf("%s: got\n%s\nwant\n%s", tc.title, got, tc.want)
		}
	}

	// Removing Orphan would break Lonely's goto.
	doc := parse(t, checksSource)
	var titles []string
	for _, f := range CheckFixes(nil, doc, style.Default().Checks) {
		titles = append(titles, f.Title)
	}
	if !slices.Contains(titles, "Remove the state `Lonely`") || slices.Contains(titles, "Remove the state `Orphan`") {
		t.Errorf("fixes %q", titles)
	}
}

func TestChecksLevels(t *testing.T) {
	doc := parse(t, checksSource)
	checks := style.Checks{
		Unused:            style.LevelOff,
		NeverSent:         style.LevelOff,
		UnreachableStates: style.LevelError,
		UnreachableCode:   style.LevelOff,
		Shadowing:         style.LevelOff,
	}
	diagnostics := Checks([]*Document{doc}, doc, checks)
	if len(diagnostics) != 2 {
		t.Fatalf("got %v", checkMessages(diagnostics))
	}
	for _, d := range diagnostics {
		if *d.Severity != protocol.DiagnosticSeverityError || len(d.Tags) != 1 {
			t.Errorf("severity %v, tags %v for %s", *d.Severity, d.Tags, d.Message)
		}
	}
}

func checkMessages(diagnostics []protocol.Diagnostic) []string {
	var out []string
	for _, d := range diagnostics {
		start := d.Range.Start
		out = append(out, fmt.Sprintf("%d:%d %s: %s", start.Line+1, start.Character+1, d.Code.Value, d.Message))
	}
	return out
}
