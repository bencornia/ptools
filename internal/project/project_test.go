package project

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeFiles creates files (relative path -> contents) under root.
func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, contents := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func paths(root string, names ...string) []string {
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = filepath.Join(root, name)
	}
	return out
}

// The layout of the P tutorials: a project that includes a shared one from a
// sibling directory.
func tutorial(t *testing.T) string {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"App/App.pproj": `<Project>
<ProjectName>App</ProjectName>
<InputFiles>
	<PFile>./PSrc/</PFile>
	<PFile> PTst\Test.p </PFile>
	<PFile>./PTst/Missing.p</PFile>
</InputFiles>
<IncludeProject>../Common/Timer/Timer.pproj</IncludeProject>
<IncludeProject>../Common/Missing/Missing.pproj</IncludeProject>
</Project>`,
		"App/PSrc/Main.p":                  "",
		"App/PSrc/nested/Types.P":          "",
		"App/PSrc/.hidden.p":               "",
		"App/PSrc/._Main.p":                "",
		"App/PSrc/.git/Ignored.p":          "",
		"App/PSrc/notes.txt":               "",
		"App/PTst/Test.p":                  "",
		"App/PTst/Unlisted.p":              "",
		"App/fixed_responses/Stray.p":      "",
		"Common/Timer/Timer.pproj":         `<Project><InputFiles><PFile>./PSrc/</PFile></InputFiles><IncludeProject>../Cycle/Cycle.pproj</IncludeProject></Project>`,
		"Common/Timer/PSrc/Timer.p":        "",
		"Common/Cycle/Cycle.pproj":         `<Project><InputFiles><PFile>Cycle.p</PFile></InputFiles><IncludeProject>../Timer/Timer.pproj</IncludeProject></Project>`,
		"Common/Cycle/Cycle.p":             "",
		"Common/Unrelated/Unrelated.pproj": `<Project><InputFiles><PFile>U.p</PFile></InputFiles><IncludeProject>../Timer/Timer.pproj</IncludeProject></Project>`,
		"Common/Unrelated/U.p":             "",
	})
	return root
}

func TestParse(t *testing.T) {
	root := tutorial(t)
	p, err := Parse(filepath.Join(root, "App/App.pproj"))
	if err != nil {
		t.Fatal(err)
	}

	wantFiles := paths(root, "App/PSrc/Main.p", "App/PSrc/nested/Types.P", "App/PTst/Test.p")
	if !reflect.DeepEqual(p.Files, wantFiles) {
		t.Errorf("Files = %v\nwant %v", p.Files, wantFiles)
	}
	wantIncludes := paths(root, "Common/Timer/Timer.pproj", "Common/Missing/Missing.pproj")
	if !reflect.DeepEqual(p.Includes, wantIncludes) {
		t.Errorf("Includes = %v\nwant %v", p.Includes, wantIncludes)
	}
}

func TestParseInvalidXML(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"Bad.pproj": "<Project><InputFiles>"})
	if _, err := Parse(filepath.Join(root, "Bad.pproj")); err == nil {
		t.Error("expected an error for truncated XML")
	}
}

func TestVisible(t *testing.T) {
	root := tutorial(t)
	x := NewIndex()
	x.Load(filepath.Join(root, "App/App.pproj"), filepath.Join(root, "Common/Unrelated/Unrelated.pproj"))

	if _, ok := x.Errors[filepath.Join(root, "Common/Missing/Missing.pproj")]; !ok || len(x.Errors) != 1 {
		t.Errorf("Errors = %v, want only the missing include", x.Errors)
	}
	wantProjects := paths(root, "App/App.pproj", "Common/Cycle/Cycle.pproj", "Common/Timer/Timer.pproj", "Common/Unrelated/Unrelated.pproj")
	if got := x.Projects(); !reflect.DeepEqual(got, wantProjects) {
		t.Errorf("Projects = %v\nwant %v", got, wantProjects)
	}

	tests := []struct {
		file string
		want []string
	}{
		// The app sees its own files and its includes, transitively.
		{"App/PSrc/Main.p", []string{
			"App/PSrc/Main.p", "App/PSrc/nested/Types.P", "App/PTst/Test.p",
			"Common/Cycle/Cycle.p", "Common/Timer/PSrc/Timer.p",
		}},
		// A shared project sees only itself and what it includes, not the
		// app or Unrelated, which both include it.
		{"Common/Timer/PSrc/Timer.p", []string{"Common/Cycle/Cycle.p", "Common/Timer/PSrc/Timer.p"}},
	}
	for _, tt := range tests {
		got, ok := x.Visible(filepath.Join(root, tt.file))
		if want := paths(root, tt.want...); !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("Visible(%s) = %v, %v\nwant %v", tt.file, got, ok, want)
		}
	}

	for _, unlisted := range []string{"App/fixed_responses/Stray.p", "App/PTst/Unlisted.p"} {
		if got, ok := x.Visible(filepath.Join(root, unlisted)); ok {
			t.Errorf("Visible(%s) = %v, want not ok", unlisted, got)
		}
	}
}

func TestFind(t *testing.T) {
	root := tutorial(t)
	pFiles, projects, err := Find(filepath.Join(root, "App"))
	if err != nil {
		t.Fatal(err)
	}
	wantP := paths(root, "App/PSrc/Main.p", "App/PSrc/nested/Types.P", "App/PTst/Test.p", "App/PTst/Unlisted.p", "App/fixed_responses/Stray.p")
	if !reflect.DeepEqual(pFiles, wantP) {
		t.Errorf("pFiles = %v\nwant %v", pFiles, wantP)
	}
	if want := paths(root, "App/App.pproj"); !reflect.DeepEqual(projects, want) {
		t.Errorf("projects = %v, want %v", projects, want)
	}
}

func TestFallbackRoot(t *testing.T) {
	tests := []struct{ file, want string }{
		{"/w/Raft/PSrc/Server.p", "/w/Raft"},
		{"/w/Raft/PSpec/Safety.p", "/w/Raft"},
		{"/w/Raft/PSrc/nested/Types.p", "/w/Raft"},
		{"/w/Raft/psrc/Server.p", "/w/Raft"},
		{"/w/Tst/Correct/Test1/main.p", "/w/Tst/Correct/Test1"},
		{"/w/App/fixed_responses/Stray.p", "/w/App/fixed_responses"},
		{"/Single.p", "/"},
	}
	for _, tt := range tests {
		file, want := filepath.FromSlash(tt.file), filepath.FromSlash(tt.want)
		if got := FallbackRoot(file); got != want {
			t.Errorf("FallbackRoot(%s) = %s, want %s", tt.file, got, want)
		}
	}
}

func TestWithin(t *testing.T) {
	tests := []struct {
		path, dir string
		want      bool
	}{
		{"/w/a/b.p", "/w/a", true},
		{"/w/a", "/w/a", true},
		{"/w/ab/c.p", "/w/a", false},
		{"/w/b.p", "/w/a", false},
		{"/w/..data/x.p", "/w", true},
	}
	for _, tt := range tests {
		if got := Within(filepath.FromSlash(tt.path), filepath.FromSlash(tt.dir)); got != tt.want {
			t.Errorf("Within(%s, %s) = %v, want %v", tt.path, tt.dir, got, tt.want)
		}
	}
}
