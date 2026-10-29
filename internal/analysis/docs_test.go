package analysis

import (
	"reflect"
	"testing"
)

const docsEvents = `// A ping from a client.
event ePing: (source: machine, n: int);
event ePong;
event eUnused;
type tCount = int;
param limit: int;
`

const docsMain = `/* Answers pings. */
machine Server {
  var count: tCount;

  // Waits for pings.
  start hot state Serving {
    entry setup;
    on ePing do (p: (source: machine, n: int)) {
      send p.source, ePong;
      goto Busy;
    }
    ignore eUnused;
  }

  state Busy {
    defer ePing;
    on null goto Serving with reset;
    exit { }
  }

  fun setup() { }
  fun reset() { receive { case ePing: { } } }
}

machine Client {
  start state Init {
    entry { send this, ePing, (source = this, n = 1); }
    on ePong goto Init;
  }
}

spec Answered observes ePing, ePong {
  start cold state Waiting { on ePing goto Waiting; }
}

test tcOne [main=Client]: assert Answered in { Client, Server };
`

func TestDocs(t *testing.T) {
	events, main := parse(t, docsEvents), parse(t, docsMain)
	p := Docs([]*Document{events, main}, []*Document{events, main})

	if got := names(p.Types); !reflect.DeepEqual(got, []string{"tCount"}) {
		t.Errorf("types %v", got)
	}
	if got := names(p.Params); !reflect.DeepEqual(got, []string{"limit"}) {
		t.Errorf("params %v", got)
	}
	if got := names(p.Tests); !reflect.DeepEqual(got, []string{"tcOne"}) || p.Tests[0].Source != "test tcOne [main=Client]: assert Answered in { Client, Server };" {
		t.Errorf("tests %+v", p.Tests)
	}

	ping := p.Events[0]
	if ping.Name != "ePing" || ping.Payload != "(source: machine, n: int)" || ping.Comment != "A ping from a client." ||
		ping.Source != "event ePing: (source: machine, n: int);" || ping.Line != 2 {
		t.Errorf("ePing %+v", ping.Doc)
	}
	wantUses := []EventUse{
		{Verb: "on", Machine: "Server", MachineKind: "machine", State: "Serving"},
		{Verb: "defer", Machine: "Server", MachineKind: "machine", State: "Busy"},
		{Verb: "receive", Machine: "Server", MachineKind: "machine", Function: "reset"},
		{Verb: "send", Machine: "Client", MachineKind: "machine", State: "Init"},
		{Verb: "observe", Machine: "Answered", MachineKind: "spec"},
		{Verb: "on", Machine: "Answered", MachineKind: "spec", State: "Waiting"},
	}
	if !reflect.DeepEqual(ping.Uses, wantUses) {
		t.Errorf("ePing uses\n got %+v\nwant %+v", ping.Uses, wantUses)
	}
	if p.Events[2].Name != "eUnused" || !reflect.DeepEqual(p.Events[2].Uses, []EventUse{{Verb: "ignore", Machine: "Server", MachineKind: "machine", State: "Serving"}}) {
		t.Errorf("eUnused %+v", p.Events[2])
	}

	if len(p.Machines) != 3 {
		t.Fatalf("machines %+v", p.Machines)
	}
	server := p.Machines[0]
	if server.Kind != "machine" || server.Comment != "Answers pings." || server.Source != "machine Server" {
		t.Errorf("Server %+v", server.Doc)
	}
	if got := names(server.Variables); !reflect.DeepEqual(got, []string{"count"}) {
		t.Errorf("variables %v", got)
	}
	if got := names(server.Functions); !reflect.DeepEqual(got, []string{"setup", "reset"}) {
		t.Errorf("functions %v", got)
	}
	serving := server.States[0]
	if serving.Name != "Serving" || !serving.Start || serving.Temperature != "hot" || serving.Comment != "Waits for pings." ||
		serving.Source != "start hot state Serving" {
		t.Errorf("Serving %+v", serving)
	}
	wantItems := []StateItem{
		{Kind: "entry", Function: "setup"},
		{Kind: "on", Events: []string{"ePing"}},
		{Kind: "ignore", Events: []string{"eUnused"}},
	}
	if !reflect.DeepEqual(serving.Items, wantItems) {
		t.Errorf("Serving items %+v", serving.Items)
	}
	wantItems = []StateItem{
		{Kind: "defer", Events: []string{"ePing"}},
		{Kind: "on", Events: []string{"null"}, Goto: "Serving", Function: "reset"},
		{Kind: "exit"},
	}
	if !reflect.DeepEqual(server.States[1].Items, wantItems) {
		t.Errorf("Busy items %+v", server.States[1].Items)
	}
	spec := p.Machines[2]
	if spec.Kind != "spec" || !reflect.DeepEqual(spec.Observes, []string{"ePing", "ePong"}) || spec.States[0].Temperature != "cold" {
		t.Errorf("spec %+v", spec)
	}
}

func names(docs []Doc) []string {
	var out []string
	for _, d := range docs {
		out = append(out, d.Name)
	}
	return out
}
