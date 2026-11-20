package server

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

func renameEdits(t *testing.T, s *Server, root, file string, pos protocol.Position, name string) []string {
	t.Helper()
	edit, err := s.rename(fakeContext(nil), &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: pathToURI(filepath.Join(root, file))},
			Position:     pos,
		},
		NewName: name,
	})
	if err != nil {
		t.Fatal(err)
	}
	if edit == nil {
		t.Errorf("%s isn't loaded", file)
		return nil
	}
	got := []string{}
	for uri, edits := range edit.Changes {
		rel, _ := filepath.Rel(root, uriToPath(uri))
		for _, e := range edits {
			got = append(got, rel+":"+itoa(int(e.Range.Start.Line)+1)+" "+e.NewText)
		}
	}
	slices.Sort(got)
	return got
}

func TestRenameAcrossRelativeInclude(t *testing.T) {
	files := map[string]string{
		"App/App.pproj":        `<Project><InputFiles><PFile>./PSrc/</PFile></InputFiles><IncludeProject>../Common/Lib/Lib.pproj</IncludeProject></Project>`,
		"App/PSrc/Main.p":      mainP,
		"Common/Lib/Lib.pproj": appProject,
		"Common/Lib/PSrc/Ev.p": "event eFoo;\n",
	}
	want := []string{"App/PSrc/Main.p:3 eBar", "Common/Lib/PSrc/Ev.p:1 eBar"}
	for _, ws := range []string{"App", "", "Common/Lib"} {
		s, root := start(t, files, client{workspace: ws})
		open(t, s, root, "App/PSrc/Main.p")
		if got := renameEdits(t, s, root, "App/PSrc/Main.p", protocol.Position{Line: 2, Character: 8}, "eBar"); !slices.Equal(got, want) {
			t.Errorf("workspace %q, from use: got %v, want %v", ws, got, want)
		}
		if got := renameEdits(t, s, root, "Common/Lib/PSrc/Ev.p", protocol.Position{Line: 0, Character: 6}, "eBar"); !slices.Equal(got, want) {
			t.Errorf("workspace %q, from decl: got %v, want %v", ws, got, want)
		}
	}
}

func TestRenameWithRelativeRootURI(t *testing.T) {
	files := map[string]string{
		"App/App.pproj":        `<Project><InputFiles><PFile>./PSrc/</PFile></InputFiles><IncludeProject>../Common/Lib/Lib.pproj</IncludeProject></Project>`,
		"App/PSrc/Main.p":      mainP,
		"Common/Lib/Lib.pproj": appProject,
		"Common/Lib/PSrc/Ev.p": "event eFoo;\n",
	}
	want := []string{"App/PSrc/Main.p:3 eBar", "Common/Lib/PSrc/Ev.p:1 eBar"}
	for _, rootURI := range []string{"file://.", "file:."} {
		root := t.TempDir()
		for name, text := range files {
			write(t, root, name, text)
		}
		t.Chdir(root)
		raw, err := json.Marshal(map[string]any{"rootUri": rootURI})
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
		open(t, s, root, "Common/Lib/PSrc/Ev.p")
		if got := renameEdits(t, s, root, "Common/Lib/PSrc/Ev.p", protocol.Position{Line: 0, Character: 6}, "eBar"); !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", rootURI, got, want)
		}
	}
}
