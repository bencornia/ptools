package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

type call struct {
	method string
	params any
}

// fakeContext records the requests the server sends to the client.
func fakeContext(calls chan<- call) *glsp.Context {
	return &glsp.Context{
		Notify: func(string, any) {},
		Call: func(method string, params any, _ any) {
			if calls != nil {
				calls <- call{method, params}
			}
		},
	}
}

type client struct {
	// watch and relative are the client's dynamicRegistration and
	// relativePatternSupport for didChangeWatchedFiles.
	watch, relative bool
	// workspace is the workspace folder, relative to the temporary
	// directory; empty means the directory itself.
	workspace string
}

// start creates files under a temporary directory and initializes a server
// on it. It returns the server and the temporary directory.
func start(t *testing.T, files map[string]string, c client) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	for name, text := range files {
		write(t, root, name, text)
	}

	raw, err := json.Marshal(map[string]any{
		"rootUri": pathToURI(filepath.Join(root, c.workspace)),
		"capabilities": map[string]any{"workspace": map[string]any{"didChangeWatchedFiles": map[string]any{
			"dynamicRegistration":    c.watch,
			"relativePatternSupport": c.relative,
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var params protocol.InitializeParams
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	ctx := fakeContext(nil)
	ctx.Params = raw
	if _, err := s.initialize(ctx, &params); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.shutdown(fakeContext(nil)) })
	return s, root
}

func write(t *testing.T, root, name, text string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func notify(t *testing.T, s *Server, root string, changes map[string]protocol.UInteger) {
	t.Helper()
	params := &protocol.DidChangeWatchedFilesParams{}
	for name, typ := range changes {
		params.Changes = append(params.Changes, protocol.FileEvent{URI: pathToURI(filepath.Join(root, name)), Type: typ})
	}
	if err := s.didChangeWatchedFiles(fakeContext(nil), params); err != nil {
		t.Fatal(err)
	}
}

// definitionOf returns "file:line" for each definition of the first
// occurrence of word in the named file.
func definitionOf(t *testing.T, s *Server, root, name, word string) []string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	if doc, ok := s.docs[filepath.Join(root, name)]; ok {
		text = doc.Text
	}
	s.mu.Unlock()

	offset := strings.Index(string(text), word)
	if offset < 0 {
		t.Fatalf("%q not in %s", word, name)
	}
	line := strings.Count(string(text[:offset]), "\n")
	col := offset - (strings.LastIndex(string(text[:offset]), "\n") + 1)

	result, err := s.definition(fakeContext(nil), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: pathToURI(filepath.Join(root, name))},
			Position:     protocol.Position{Line: protocol.UInteger(line), Character: protocol.UInteger(col)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	locs, _ := result.([]protocol.Location)
	got := []string{}
	for _, loc := range locs {
		rel, _ := filepath.Rel(root, uriToPath(loc.URI))
		got = append(got, rel+":"+itoa(int(loc.Range.Start.Line)+1))
	}
	return got
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func assertDefinitions(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("definitions = %v, want %v", got, want)
	}
}

const appProject = `<Project><InputFiles><PFile>./PSrc/</PFile></InputFiles></Project>`

const mainP = "machine M {\n  start state S {\n    on eFoo do {}\n  }\n}\n"

func nextCall(t *testing.T, calls <-chan call) call {
	t.Helper()
	select {
	case c := <-calls:
		return c
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a request to the client")
		return call{}
	}
}

// registeredPatterns checks that c registers file watchers and returns their
// patterns, as sent over the wire. A RelativePattern is rendered as
// "<base dir relative to root>/<pattern>".
func registeredPatterns(t *testing.T, c call, root string) []string {
	t.Helper()
	if c.method != "client/registerCapability" {
		t.Fatalf("got %s, want client/registerCapability", c.method)
	}
	raw, err := json.Marshal(c.params)
	if err != nil {
		t.Fatal(err)
	}
	var params struct {
		Registrations []struct {
			Method          string `json:"method"`
			RegisterOptions struct {
				Watchers []struct {
					GlobPattern json.RawMessage `json:"globPattern"`
				} `json:"watchers"`
			} `json:"registerOptions"`
		} `json:"registrations"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatal(err)
	}
	if len(params.Registrations) != 1 || params.Registrations[0].Method != "workspace/didChangeWatchedFiles" {
		t.Fatalf("unexpected registration: %s", raw)
	}

	patterns := []string{}
	for _, w := range params.Registrations[0].RegisterOptions.Watchers {
		var glob string
		if json.Unmarshal(w.GlobPattern, &glob) == nil {
			patterns = append(patterns, glob)
			continue
		}
		var rel struct {
			BaseURI string `json:"baseUri"`
			Pattern string `json:"pattern"`
		}
		if err := json.Unmarshal(w.GlobPattern, &rel); err != nil {
			t.Fatalf("bad glob pattern %s", w.GlobPattern)
		}
		base, _ := filepath.Rel(root, uriToPath(rel.BaseURI))
		patterns = append(patterns, base+"/"+rel.Pattern)
	}
	return patterns
}

func assertPatterns(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("watched %v, want %v", got, want)
	}
}

func TestRegistersFileWatchers(t *testing.T) {
	s, root := start(t, nil, client{watch: true})
	calls := make(chan call, 1)
	if err := s.initialized(fakeContext(calls), &protocol.InitializedParams{}); err != nil {
		t.Fatal(err)
	}
	assertPatterns(t, registeredPatterns(t, nextCall(t, calls), root), "**/*.p", "**/*.pproj")
}

func TestNoWatchersWithoutClientSupport(t *testing.T) {
	s, _ := start(t, nil, client{})
	calls := make(chan call, 1)
	if err := s.initialized(fakeContext(calls), &protocol.InitializedParams{}); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-calls:
		t.Errorf("unexpected request to client: %s", c.method)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestProjectChange(t *testing.T) {
	s, root := start(t, map[string]string{
		"App/App.pproj":     appProject,
		"App/PSrc/Main.p":   mainP,
		"App/Extra/Event.p": "event eFoo;\n",
	}, client{watch: true})
	assertDefinitions(t, definitionOf(t, s, root, "App/PSrc/Main.p", "eFoo"))

	write(t, root, "App/App.pproj", `<Project><InputFiles><PFile>./PSrc/</PFile><PFile>./Extra/</PFile></InputFiles></Project>`)
	notify(t, s, root, map[string]protocol.UInteger{"App/App.pproj": protocol.FileChangeTypeChanged})
	assertDefinitions(t, definitionOf(t, s, root, "App/PSrc/Main.p", "eFoo"), "App/Extra/Event.p:1")
}

func TestCreatedAndDeletedFiles(t *testing.T) {
	s, root := start(t, map[string]string{
		"App/App.pproj":   appProject,
		"App/PSrc/Main.p": mainP,
	}, client{watch: true})
	assertDefinitions(t, definitionOf(t, s, root, "App/PSrc/Main.p", "eFoo"))

	write(t, root, "App/PSrc/Events.p", "event eFoo;\n")
	notify(t, s, root, map[string]protocol.UInteger{"App/PSrc/Events.p": protocol.FileChangeTypeCreated})
	assertDefinitions(t, definitionOf(t, s, root, "App/PSrc/Main.p", "eFoo"), "App/PSrc/Events.p:1")

	if err := os.Remove(filepath.Join(root, "App/PSrc/Events.p")); err != nil {
		t.Fatal(err)
	}
	notify(t, s, root, map[string]protocol.UInteger{"App/PSrc/Events.p": protocol.FileChangeTypeDeleted})
	assertDefinitions(t, definitionOf(t, s, root, "App/PSrc/Main.p", "eFoo"))
	if _, loaded := s.docs[filepath.Join(root, "App/PSrc/Events.p")]; loaded {
		t.Error("deleted file is still loaded")
	}
}

func TestChangedFileIsReparsed(t *testing.T) {
	s, root := start(t, map[string]string{
		"App/App.pproj":    appProject,
		"App/PSrc/Main.p":  mainP,
		"App/PSrc/Event.p": "event eFoo;\n",
	}, client{watch: true})
	assertDefinitions(t, definitionOf(t, s, root, "App/PSrc/Main.p", "eFoo"), "App/PSrc/Event.p:1")

	write(t, root, "App/PSrc/Event.p", "event eBar;\n\nevent eFoo;\n")
	notify(t, s, root, map[string]protocol.UInteger{"App/PSrc/Event.p": protocol.FileChangeTypeChanged})
	assertDefinitions(t, definitionOf(t, s, root, "App/PSrc/Main.p", "eFoo"), "App/PSrc/Event.p:3")
}

func TestOpenFileKeepsEditorText(t *testing.T) {
	s, root := start(t, map[string]string{
		"App/App.pproj":    appProject,
		"App/PSrc/Main.p":  mainP,
		"App/PSrc/Event.p": "event eFoo;\n",
	}, client{watch: true})

	// Unsaved edits move the declaration to line 2.
	uri := pathToURI(filepath.Join(root, "App/PSrc/Event.p"))
	if err := s.didOpen(fakeContext(nil), &protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{
		URI: uri, LanguageID: "p", Version: 1, Text: "\nevent eFoo;\n",
	}}); err != nil {
		t.Fatal(err)
	}

	// Another process rewrites the file, and a project file changes too.
	write(t, root, "App/PSrc/Event.p", "\n\n\nevent eFoo;\n")
	notify(t, s, root, map[string]protocol.UInteger{
		"App/PSrc/Event.p": protocol.FileChangeTypeChanged,
		"App/App.pproj":    protocol.FileChangeTypeChanged,
	})
	assertDefinitions(t, definitionOf(t, s, root, "App/PSrc/Main.p", "eFoo"), "App/PSrc/Event.p:2")
}
