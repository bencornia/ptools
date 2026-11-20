package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// publishingContext records the diagnostics the server publishes.
func publishingContext(published chan<- protocol.PublishDiagnosticsParams) *glsp.Context {
	return &glsp.Context{
		Notify: func(method string, params any) {
			if method == string(protocol.ServerTextDocumentPublishDiagnostics) {
				published <- params.(protocol.PublishDiagnosticsParams)
			}
		},
		Call: func(string, any, any) {},
	}
}

// withFakeCompiler gives s a compiler that prints the contents of the file
// it returns, and records each run in a log next to it.
func withFakeCompiler(t *testing.T, s *Server) (output, runs string) {
	t.Helper()
	dir := t.TempDir()
	output, runs = filepath.Join(dir, "output"), filepath.Join(dir, "runs")
	script := filepath.Join(dir, "fake-p")
	body := fmt.Sprintf("#!/bin/sh\necho \"$@\" >> %s\ncat %s 2>/dev/null\n", runs, output)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	s.SetCompiler([]string{script}, 10*time.Second)
	if s.compiler == nil {
		t.Fatal("fake compiler not available")
	}
	return output, runs
}

func save(t *testing.T, s *Server, ctx *glsp.Context, path string) {
	t.Helper()
	if err := s.didSave(ctx, &protocol.DidSaveTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: pathToURI(path)},
	}); err != nil {
		t.Fatal(err)
	}
}

// collect waits for diagnostics to be published for n files, and returns
// them rendered as "file:line:col-col message" per diagnostic, by file.
func collect(t *testing.T, published <-chan protocol.PublishDiagnosticsParams, root string, n int) map[string][]string {
	t.Helper()
	got := map[string][]string{}
	for range n {
		select {
		case p := <-published:
			rel, _ := filepath.Rel(root, uriToPath(p.URI))
			list := []string{}
			for _, d := range p.Diagnostics {
				source := ""
				if d.Source != nil {
					source = *d.Source + ": "
				}
				list = append(list, fmt.Sprintf("%d:%d-%d %s%s", d.Range.Start.Line+1, d.Range.Start.Character+1, d.Range.End.Character+1, source, d.Message))
			}
			got[rel] = list
		case <-time.After(5 * time.Second):
			t.Fatalf("got diagnostics for %d files, waited for %d: %v", len(got), n, got)
		}
	}
	return got
}

func TestSaveTypeChecksTheProject(t *testing.T) {
	s, root := start(t, map[string]string{
		"App/App.pproj":     appProject,
		"App/PSrc/Main.p":   mainP,
		"App/PSrc/Other.p":  "event eOther;\n",
		"Stray/Stray.p":     mainP,
		"Stray/Stray.pproj": "<Project></Project>",
	}, client{})
	output, runs := withFakeCompiler(t, s)
	published := make(chan protocol.PublishDiagnosticsParams, 10)
	ctx := publishingContext(published)

	// Line 3 of Main.p is "    on eFoo do {}"; column 8 is eFoo.
	os.WriteFile(output, []byte("Type checking ...\n"+
		"[Error:] [PSrc/Main.p:3:8] could not find event 'eFoo'\n"+
		"[Error:] [PSrc/Other.p:1:1] another error\n"+
		"[Error:] [<no source>:-1:-1] an error with no location\n"), 0o644)
	save(t, s, ctx, filepath.Join(root, "App/PSrc/Main.p"))
	got := collect(t, published, root, 3)
	want := map[string][]string{
		"App/PSrc/Main.p":  {"3:8-12 p compile: could not find event 'eFoo'"},
		"App/PSrc/Other.p": {"1:1-6 p compile: another error"},
		"App/App.pproj":    {"1:1-1 p compile: an error with no location"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("published\n got %v\nwant %v", got, want)
	}

	// The compiler ran once, on the project, from its directory.
	data, _ := os.ReadFile(runs)
	if lines := strings.Split(strings.TrimSpace(string(data)), "\n"); len(lines) != 1 ||
		!strings.HasPrefix(lines[0], "compile --pproj "+filepath.Join(root, "App/App.pproj")+" --outdir ") {
		t.Errorf("compiler runs: %q", data)
	}

	// A clean compile clears them.
	os.WriteFile(output, []byte("Type checking ...\nCode generation for PChecker...\n"), 0o644)
	save(t, s, ctx, filepath.Join(root, "App/PSrc/Other.p"))
	got = collect(t, published, root, 3)
	for file, diags := range got {
		if len(diags) != 0 {
			t.Errorf("%s still has %v after a clean compile", file, diags)
		}
	}
}

func TestOpenFileShowsSyntaxAndTypeErrors(t *testing.T) {
	s, root := start(t, map[string]string{
		"App/App.pproj":     appProject,
		"App/PSrc/Events.p": "event eFoo;\n",
		"App/PSrc/Main.p":   mainP,
	}, client{})
	output, _ := withFakeCompiler(t, s)
	published := make(chan protocol.PublishDiagnosticsParams, 10)
	ctx := publishingContext(published)
	main := filepath.Join(root, "App/PSrc/Main.p")

	// An error only the compiler finds.
	os.WriteFile(output, []byte("[Error:] [PSrc/Main.p:3:8] could not find event 'eFoo'\n"), 0o644)
	save(t, s, ctx, main)
	collect(t, published, root, 1)

	// Typing a syntax error keeps the type error from the last compile.
	if err := s.didOpen(ctx, &protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{
		URI: pathToURI(main), LanguageID: "p", Version: 1, Text: mainP + "event\n",
	}}); err != nil {
		t.Fatal(err)
	}
	got := collect(t, published, root, 1)["App/PSrc/Main.p"]
	if len(got) != 2 || !strings.Contains(got[0], "plsp:") || !strings.Contains(got[1], "p compile: could not find event") {
		t.Errorf("got %v, want a syntax error then the type error", got)
	}

	// Closing drops the syntax error but keeps the type error.
	if err := s.didClose(ctx, &protocol.DidCloseTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: pathToURI(main)}}); err != nil {
		t.Fatal(err)
	}
	got = collect(t, published, root, 1)["App/PSrc/Main.p"]
	if len(got) != 1 || !strings.Contains(got[0], "p compile:") {
		t.Errorf("after closing, got %v, want only the type error", got)
	}
}

func TestFilesInNoProjectAreNotTypeChecked(t *testing.T) {
	s, root := start(t, map[string]string{
		"Examples/One/PSrc/Main.p": mainP,
	}, client{})
	_, runs := withFakeCompiler(t, s)
	save(t, s, publishingContext(make(chan protocol.PublishDiagnosticsParams, 10)), filepath.Join(root, "Examples/One/PSrc/Main.p"))
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(runs); !os.IsNotExist(err) {
		t.Error("the compiler ran for a file in no project")
	}
}

func TestMissingCompilerTurnsTypeCheckingOff(t *testing.T) {
	s, _ := start(t, nil, client{})
	s.SetCompiler([]string{"plsp-no-such-compiler"}, time.Second)
	if s.compiler != nil {
		t.Error("type checking is on without a compiler")
	}
}

func TestCompilerErrorsFoundWhileEditingAreNotRepeated(t *testing.T) {
	s, root := start(t, map[string]string{
		"App/App.pproj":   appProject,
		"App/PSrc/Main.p": mainP,
	}, client{})
	output, _ := withFakeCompiler(t, s)
	published := make(chan protocol.PublishDiagnosticsParams, 10)
	ctx := publishingContext(published)
	main := filepath.Join(root, "App/PSrc/Main.p")

	// eFoo is undefined, which the server finds itself.
	os.WriteFile(output, []byte("[Error:] [PSrc/Main.p:3:8] could not find event 'eFoo'\n"), 0o644)
	if err := s.didOpen(ctx, &protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{
		URI: pathToURI(main), LanguageID: "p", Version: 1, Text: mainP,
	}}); err != nil {
		t.Fatal(err)
	}
	collect(t, published, root, 1)
	save(t, s, ctx, main)
	got := collect(t, published, root, 1)["App/PSrc/Main.p"]
	if len(got) != 1 || !strings.Contains(got[0], "plsp: undefined event `eFoo`") {
		t.Errorf("got %v, want only the server's own report", got)
	}

	// Once the file is closed, the compiler's is all there is.
	if err := s.didClose(ctx, &protocol.DidCloseTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: pathToURI(main)}}); err != nil {
		t.Fatal(err)
	}
	got = collect(t, published, root, 1)["App/PSrc/Main.p"]
	if len(got) != 1 || !strings.Contains(got[0], "p compile: could not find event") {
		t.Errorf("after closing, got %v", got)
	}
}
