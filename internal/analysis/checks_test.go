package analysis

import (
	"fmt"
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
