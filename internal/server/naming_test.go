package server

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
	if actions := actionsAt(1, 6); len(actions) != 0 {
		t.Errorf("offered %q, which clashes with machine Client", actions[0].Title)
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
