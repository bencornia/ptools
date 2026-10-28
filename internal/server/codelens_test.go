package server

import (
	"path/filepath"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

func TestCodeLens(t *testing.T) {
	s, root := start(t, map[string]string{
		"App/App.pproj": appProject,
		"App/PSrc/Test.p": "machine Main { start state Init { } }\n" +
			"test single [main=Main]: { Main };\n" +
			"param n: int;\n" +
			"test param (n in [1, 2]) each [main=Main]:\n  { Main };\n",
		"Loose.p": "test loose [main=Main]: { Main };\n",
	}, client{})
	lenses := func(name string) []protocol.CodeLens {
		t.Helper()
		got, err := s.codeLens(fakeContext(nil), &protocol.CodeLensParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: pathToURI(filepath.Join(root, name))},
		})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	got := lenses("App/PSrc/Test.p")
	if len(got) != 4 {
		t.Fatalf("lenses = %+v, want Run and Debug for two tests", got)
	}
	file, project := filepath.Join(root, "App/PSrc/Test.p"), filepath.Join(root, "App")
	want := []struct {
		line           uint32
		title, command string
		arg            TestArgument
	}{
		{1, "Run test", RunTestCommand, TestArgument{"single", file, project}},
		{1, "Debug test", DebugTestCommand, TestArgument{"single", file, project}},
		{3, "Run test", RunTestCommand, TestArgument{"each", file, project}},
		{3, "Debug test", DebugTestCommand, TestArgument{"each", file, project}},
	}
	for i, w := range want {
		l := got[i]
		if l.Range.Start.Line != w.line || l.Range.Start.Character != 0 || l.Command.Title != w.title ||
			l.Command.Command != w.command || l.Command.Arguments[0] != w.arg {
			t.Errorf("lens %d = %v %+v, want line %d %s %+v", i, l.Range, *l.Command, w.line, w.title, w.arg)
		}
	}

	// A file no project lists has no project to compile it with.
	if got := lenses("Loose.p"); len(got) != 2 || got[0].Command.Arguments[0].(TestArgument).Project != "" {
		t.Errorf("loose lenses = %+v", got)
	}
}
