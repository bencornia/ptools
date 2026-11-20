package server

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bencornia/ptools/internal/style"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func namingWarnings(s *Server, path string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var got []string
	for _, d := range s.diagnosticsFor(path) {
		if d.Code != nil && d.Code.Value == "naming" {
			got = append(got, d.Message)
		}
	}
	return got
}

func TestNamingWarningsAndRenameFix(t *testing.T) {
	s, root := start(t, map[string]string{
		"App/App.pproj":     appProject,
		"App/PSrc/Events.p": "event lookup_done;\ntype client = int;\n",
		"App/PSrc/Main.p":   "machine Client {\n  start state Init {\n    on lookup_done do {}\n  }\n}\n",
	}, client{})
	events := filepath.Join(root, "App/PSrc/Events.p")
	open(t, s, root, "App/PSrc/Events.p")

	want := []string{
		"event name `lookup_done` should be TitleCase: `LookupDone`",
		"type name `client` should be TitleCase: `Client`",
	}
	if got := namingWarnings(s, events); !slices.Equal(got, want) {
		t.Errorf("warnings = %q, want %q", got, want)
	}

	actionsAt := func(line, char uint32) []protocol.CodeAction {
		t.Helper()
		at := protocol.Position{Line: line, Character: char}
		result, err := s.codeAction(fakeContext(nil), &protocol.CodeActionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: pathToURI(events)},
			Range:        protocol.Range{Start: at, End: at},
		})
		if err != nil {
			t.Fatal(err)
		}
		return result.([]protocol.CodeAction)
	}

	// The event is renamed everywhere it's used.
	actions := actionsAt(0, 8)
	if len(actions) != 1 || actions[0].Title != "Rename `lookup_done` to `LookupDone`" {
		t.Fatalf("actions = %+v", actions)
	}
	var got []string
	for uri, edits := range actions[0].Edit.Changes {
		rel, _ := filepath.Rel(root, uriToPath(uri))
		for _, e := range edits {
			got = append(got, rel+":"+itoa(int(e.Range.Start.Line)+1)+" "+e.NewText)
		}
	}
	slices.Sort(got)
	if want := []string{"App/PSrc/Events.p:1 LookupDone", "App/PSrc/Main.p:3 LookupDone"}; !slices.Equal(got, want) {
		t.Errorf("edits = %v, want %v", got, want)
	}

	// Renaming the type would clash with the machine, so there's no fix.
	for _, a := range actionsAt(1, 6) {
		if strings.HasPrefix(a.Title, "Rename") {
			t.Errorf("offered %q, which clashes with machine Client", a.Title)
		}
	}

	// A .plint.toml above the file changes the style.
	write(t, root, ".plint.toml", "[naming]\nevents = \"snake_case\"\ntypes = \"off\"\n")
	if got := namingWarnings(s, events); len(got) != 0 {
		t.Errorf("with .plint.toml, warnings = %q", got)
	}
	write(t, root, ".plint.toml", "[naming]\nevents = \"wrong\"\n")
	if got := namingWarnings(s, events); len(got) != 2 || !strings.Contains(s.styleErrors[filepath.Join(root, ".plint.toml")], "wrong") {
		t.Errorf("with a bad .plint.toml, warnings = %q and errors %v; want the default's", got, s.styleErrors)
	}
}

func TestCheckWarnings(t *testing.T) {
	s, root := start(t, map[string]string{
		"App/App.pproj":     appProject,
		"App/PSrc/Events.p": "event Ping;\nevent Pong;\n",
		"App/PSrc/Main.p":   "machine Client {\n  start state Init {\n    entry { send this, Pong; }\n    on Ping, Pong do {}\n  }\n}\n",
	}, client{})
	events := filepath.Join(root, "App/PSrc/Events.p")
	open(t, s, root, "App/PSrc/Events.p")

	checks := func() []string {
		s.mu.Lock()
		defer s.mu.Unlock()
		var got []string
		for _, d := range s.diagnosticsFor(events) {
			if d.Code != nil && d.Code.Value != "naming" {
				got = append(got, fmt.Sprintf("%v: %s", d.Code.Value, d.Message))
			}
		}
		return got
	}
	// Pong is sent from Main.p.
	want := []string{"never-sent: event `Ping` is never sent, raised or announced"}
	if got := checks(); !slices.Equal(got, want) {
		t.Errorf("checks = %q, want %q", got, want)
	}

	if err := os.WriteFile(filepath.Join(root, "App", style.FileName), []byte("[checks]\nnever_sent = \"off\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := checks(); len(got) > 0 {
		t.Errorf("turned off, checks = %q", got)
	}
}

func TestOrderFix(t *testing.T) {
	s, root := start(t, map[string]string{
		"App/App.pproj":   appProject,
		"App/PSrc/Main.p": "machine Client {\n  state Done { }\n  start state Init {\n    goto Done;\n  }\n}\n",
	}, client{})
	main := filepath.Join(root, "App/PSrc/Main.p")
	open(t, s, root, "App/PSrc/Main.p")
	at := protocol.Position{Line: 2, Character: 14}
	result, err := s.codeAction(fakeContext(nil), &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: pathToURI(main)},
		Range:        protocol.Range{Start: at, End: at},
	})
	if err != nil {
		t.Fatal(err)
	}
	actions := result.([]protocol.CodeAction)
	if len(actions) != 1 || actions[0].Title != "Move `Init` before `Done`" {
		t.Fatalf("actions = %+v", actions)
	}
	if got := applyEdits(t, s, main, actions[0].Edit.Changes[pathToURI(main)]); got != "machine Client {\n  start state Init {\n    goto Done;\n  }\n  state Done { }\n}\n" {
		t.Errorf("fixed = %q", got)
	}
}
