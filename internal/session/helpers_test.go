package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bencornia/ptools/internal/source"
	"github.com/bencornia/ptools/internal/trace"
)

// A project whose machine sends through a top-level helper from an
// included project, one that sends whatever event it's given, and halts.
var helperProject = map[string]string{
	"App/App.pproj": `<Project><ProjectName>App</ProjectName><InputFiles><PFile>./PSrc/</PFile></InputFiles>` +
		`<IncludeProject>../Lib/Lib.pproj</IncludeProject></Project>`,
	"App/PSrc/App.p": `event eX;
machine A {
    start state S {
        entry {
            Relay(this, eX);
            raise halt;
        }
    }
}
`,
	"Lib/Lib.pproj": `<Project><ProjectName>Lib</ProjectName><InputFiles><PFile>./Relay.p</PFile></InputFiles></Project>`,
	"Lib/Relay.p": `fun Relay(m: machine, e: event) {
    send m, e;
}
`,
}

const helperTrace = `[
	{"type": "CreateStateMachine", "details": {"id": "A(1)", "log": "A(1) was created"}},
	{"type": "StateTransition", "details": {"id": "A(1)", "state": "S", "isEntry": true, "log": "A(1) enters state 'S'"}},
	{"type": "SendEvent", "details": {"sender": "A(1)", "target": "A(1)", "event": "eX", "state": "S", "log": "sent eX"}},
	{"type": "RaiseEvent", "details": {"id": "A(1)", "event": "PHalt", "state": "S", "log": "raised PHalt"}}
]`

func TestHelpersInIncludedProjects(t *testing.T) {
	root := t.TempDir()
	for name, text := range helperProject {
		path := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	steps, err := trace.Parse([]byte(helperTrace))
	if err != nil {
		t.Fatal(err)
	}
	src, err := source.Load(filepath.Join(root, "App"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if _, ok := src.Functions["Relay"]; !ok {
		t.Fatal("the included project's function isn't indexed")
	}

	s := New(steps, src)
	for i, want := range map[int]string{
		2: "Lib/Relay.p:2", // the helper's send of whatever event it's given
		3: "App/PSrc/App.p:6",
	} {
		w := s.Where[i]
		rel, _ := filepath.Rel(root, w.File)
		if got := filepath.ToSlash(rel) + ":" + itoa(w.Line); got != want {
			t.Errorf("step %d (%s) at %s, want %s", i, steps[i].Log, got, want)
		}
	}
}

func itoa(n int) string {
	b := []byte{}
	for n > 0 || len(b) == 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
