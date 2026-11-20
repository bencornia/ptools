package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoc(t *testing.T) {
	dir := t.TempDir()
	for name, text := range map[string]string{
		"App/App.pproj": "<Project><ProjectName>App</ProjectName><InputFiles><PFile>./PSrc/</PFile></InputFiles>" +
			"<IncludeProject>../Timer/Timer.pproj</IncludeProject></Project>",
		"App/README.adoc":    "= The App\n:toc:\n\nA bank. It has clients.\n",
		"App/PSrc/App.p":     "machine Server { start state S { on eTimeOut goto S; } }\n",
		"Timer/Timer.pproj":  "<Project><InputFiles><PFile>./PSrc/</PFile></InputFiles></Project>",
		"Timer/PSrc/Timer.p": "event eTimeOut;\nmachine Timer { start state S { entry { send this, eTimeOut; } } }\n",
		"Loose/NoProject.p":  "event eLoose;\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var stdout, stderr strings.Builder
	out := filepath.Join(dir, "docs")
	if status := run([]string{"doc", "-o", out, dir}, nil, &stdout, &stderr); status != 0 || stdout.Len() > 0 || stderr.Len() > 0 {
		t.Fatalf("doc -o = %d, stdout %q, stderr %q", status, stdout.String(), stderr.String())
	}
	read := func(name string) string {
		t.Helper()
		text, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(text)
	}
	app := read("App.adoc")
	// The README's title and attributes give way to the page's own.
	if !strings.HasPrefix(app, "= App\n\nA bank. It has clients.\n\nIncludes: xref:Timer.adoc[Timer]\n\n") ||
		!strings.Contains(app, "* *on* xref:Timer.adoc#event-eTimeOut[`+eTimeOut+`]") {
		t.Errorf("App.adoc:\n%s", app)
	}
	if timer := read("Timer.adoc"); !strings.Contains(timer, "[[event-eTimeOut]]") {
		t.Errorf("Timer.adoc:\n%s", timer)
	}
	if index := read("index.adoc"); !strings.Contains(index, "|xref:App.adoc[App]\n|`+App+`\n|A bank.\n") {
		t.Errorf("index.adoc:\n%s", index)
	}

	// One project prints its page.
	stdout.Reset()
	if status := run([]string{"doc", filepath.Join(dir, "Timer")}, nil, &stdout, &stderr); status != 0 || !strings.HasPrefix(stdout.String(), "= Timer\n") {
		t.Errorf("doc Timer = %d, stdout %q, stderr %q", status, stdout.String(), stderr.String())
	}

	for _, args := range [][]string{
		{dir},                         // several projects, without -o
		{filepath.Join(dir, "Loose")}, // none
		{filepath.Join(dir, "Loose/NoProject.p")}, // not a project
	} {
		stdout.Reset()
		stderr.Reset()
		if status := run(append([]string{"doc"}, args...), nil, &stdout, &stderr); status != 2 || stdout.Len() > 0 || stderr.Len() == 0 {
			t.Errorf("doc %v = %d, stdout %q, stderr %q", args, status, stdout.String(), stderr.String())
		}
	}
}
