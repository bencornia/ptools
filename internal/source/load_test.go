package source

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadReadsWhatTheProjectCompiles(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		// A backslash path, as projects written on Windows have.
		"App/App.pproj": `<Project><InputFiles><PFile>./PSrc/</PFile></InputFiles>` +
			`<IncludeProject>..\Lib\Lib.pproj</IncludeProject></Project>`,
		"App/PSrc/App.p": "machine App { start state S {} }\nfun Shared() {}\n",
		// Not listed in <InputFiles>, so not compiled.
		"App/Old/App.p":  "machine Stale { start state S {} }\n",
		"Lib/Lib.pproj":  `<Project><InputFiles><PFile>Lib.p</PFile></InputFiles></Project>`,
		"Lib/Lib.p":      "fun Shared() {}\nfun Relay() {}\n",
		"Lib/Unlisted.p": "machine Unlisted { start state S {} }\n",
	})
	x, err := Load(filepath.Join(root, "App"))
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()

	var got []string
	for _, f := range x.Files {
		rel, _ := filepath.Rel(root, f.Path)
		got = append(got, filepath.ToSlash(rel))
	}
	// The project's own files first, then its included projects'.
	if want := []string{"App/PSrc/App.p", "Lib/Lib.p"}; !slices.Equal(got, want) {
		t.Errorf("loaded %v, want %v", got, want)
	}
	for _, name := range []string{"Stale", "Unlisted"} {
		if _, ok := x.Machines[name]; ok {
			t.Errorf("machine %s from a file no project lists is indexed", name)
		}
	}
	if _, ok := x.Functions["Relay"]; !ok {
		t.Error("the included project's function isn't indexed")
	}
}

func TestLoadWithoutAProject(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"A.p":              "machine A { start state S {} }\n",
		"PGenerated/Gen.p": "machine Gen { start state S {} }\n",
		"sub/B.p":          "machine B { start state S {} }\n",
	})
	x, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	for name, want := range map[string]bool{"A": true, "B": true, "Gen": false} {
		if _, ok := x.Machines[name]; ok != want {
			t.Errorf("machine %s indexed: %v, want %v", name, ok, want)
		}
	}
}

func TestLoadReportsABrokenProject(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"Bad.pproj": "<Project><InputFiles>"})
	if _, err := Load(root); err == nil {
		t.Error("Load of a project that isn't XML succeeded")
	}
}
