package doc

import (
	"regexp"
	"strings"
	"testing"

	"github.com/bencornia/p-lsp/internal/analysis"
)

const timerSource = `// A timeout from a timer.
event eTimeOut;
event eStart;

// Fires eTimeOut when asked to.
machine Timer {
  var client: machine;
  start state Idle {
    on eStart goto Running;
  }
  state Running {
    entry fire;
  }
  fun fire() { send client, eTimeOut; }
}
`

const appSource = `event ePing: int;

machine Server {
  start state Serving {
    on ePing do { }
    on eTimeOut goto Serving;
    defer eStart;
  }
}

spec Watch observes ePing {
  start hot state Waiting { }
}

test tcOne [main=Server]: assert Watch in { Server };
`

// project parses sources, by file name, into a project's documentation,
// against its own files and those of includes.
func project(t *testing.T, name string, sources map[string]string, includes ...*Project) *Project {
	t.Helper()
	parser := analysis.NewParser()
	t.Cleanup(parser.Close)
	p := &Project{Name: name, Dir: "/src/" + name, Path: "/src/" + name + "/" + name + ".pproj"}
	var docs []*analysis.Document
	for file, src := range sources {
		path := p.Dir + "/" + file
		d := analysis.Parse(parser, path, 0, src)
		t.Cleanup(d.Close)
		docs = append(docs, d)
		p.Files = append(p.Files, path)
	}
	workspace := docs
	for _, inc := range includes {
		p.Includes = append(p.Includes, inc.Path)
		for file, src := range map[string]string{"Timer.p": timerSource} {
			d := analysis.Parse(parser, inc.Dir+"/"+file, 0, src)
			t.Cleanup(d.Close)
			workspace = append(workspace, d)
		}
	}
	p.Package = analysis.Docs(docs, workspace)
	return p
}

func render(t *testing.T, s *Site, p *Project) string {
	t.Helper()
	var b strings.Builder
	if err := s.WritePage(&b, p); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestPage(t *testing.T) {
	timer := project(t, "Timer", map[string]string{"Timer.p": timerSource})
	timer.Overview = "A timer.\n\n== Using it\n\nCreate one."
	app := project(t, "App", map[string]string{"App.p": appSource}, timer)
	site := NewSite("Docs", []*Project{app, timer}, false)

	page := render(t, site, timer)
	for _, want := range []string{
		"= Timer\n\nA timer.\n\n== Using it\n\nCreate one.\n\n",
		// eStart is handled only by Timer, so it's listed under it.
		"** <<machine-Timer,`+Timer+`>>\n*** accepts <<event-eStart,`+eStart+`>>\n",
		"[[machine-Timer]]\n=== `+Timer+`\n\n[source,p]\n----\nmachine Timer\n----\n\nFires eTimeOut when asked to.\n\n==== Accepts\n\n[[event-eStart]]\n===== `+eStart+`",
		// No machine handles eTimeOut in Timer's own project.
		"[[events]]\n== Events\n\n[[event-eTimeOut]]\n=== `+eTimeOut+`\n\n[source,p]\n----\nevent eTimeOut;\n----\n\nA timeout from a timer.\n\n" +
			"[horizontal]\nSent by:: <<machine-Timer,`+Timer+`>> in `+fire+`\n",
		"[[machine-Timer-state-Idle]]\n===== `+Idle+`\n\n_Start state._\n\n* *on* <<event-eStart,`+eStart+`>> *goto* <<machine-Timer-state-Running,`+Running+`>>\n",
		// Without -private, functions aren't linked.
		"* *entry* `+fire+`\n",
		"[[files]]\n== Files\n\n* `+Timer.p+`\n",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("Timer's page lacks\n%s\n\npage:\n%s", want, page)
		}
	}
	if strings.Contains(page, "Variables") {
		t.Errorf("private members shown:\n%s", page)
	}
	checkAnchors(t, page)

	page = render(t, site, app)
	for _, want := range []string{
		"Includes: xref:Timer.adoc[Timer]\n",
		// The included project's events link to its page.
		"* *on* xref:Timer.adoc#event-eTimeOut[`+eTimeOut+`] *goto* <<machine-Server-state-Serving,`+Serving+`>>\n",
		"* *defer* xref:Timer.adoc#event-eStart[`+eStart+`]\n",
		"* *on* <<event-ePing,`+ePing+`>> *do* `+{ … }+`\n",
		"[[specs]]\n== Specs\n\n[[machine-Watch]]\n=== `+Watch+`\n\n[source,p]\n----\nspec Watch observes ePing\n----\n\nObserves: <<event-ePing,`+ePing+`>>\n",
		"_Start state, hot._",
		"[[test-tcOne]]\n=== `+tcOne+`\n\n[source,p]\n----\ntest tcOne [main=Server]: assert Watch in { Server };\n----\n",
		// A spec's handlers don't make it a handler.
		"[horizontal]\nHandled by:: <<machine-Server,`+Server+`>> in <<machine-Server-state-Serving,`+Serving+`>>\nObserved by:: <<machine-Watch,`+Watch+`>>\n",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("App's page lacks\n%s\n\npage:\n%s", want, page)
		}
	}
	checkAnchors(t, page)

	var index strings.Builder
	if err := site.WriteIndex(&index); err != nil {
		t.Fatal(err)
	}
	if want := "= Docs\n\n[cols=\"2,2,3\"]\n|===\n|Project |Directory |Synopsis\n\n" +
		"|xref:App.adoc[App]\n|`+App+`\n|\n\n|xref:Timer.adoc[Timer]\n|`+Timer+`\n|A timer.\n\n|===\n"; index.String() != want {
		t.Errorf("index\n%s\nwant\n%s", index.String(), want)
	}
}

func TestPrivate(t *testing.T) {
	timer := project(t, "Timer", map[string]string{"Timer.p": timerSource})
	page := render(t, NewSite("Docs", []*Project{timer}, true), timer)
	for _, want := range []string{
		"* *entry* <<machine-Timer-function-fire,`+fire+`>>\n",
		"==== Variables\n\n[[machine-Timer-var-client]]\n===== `+client+`\n\n[source,p]\n----\nvar client: machine\n----\n",
		"[horizontal]\nSent by:: <<machine-Timer,`+Timer+`>> in <<machine-Timer-function-fire,`+fire+`>>\n",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks\n%s\n\npage:\n%s", want, page)
		}
	}
	checkAnchors(t, page)
}

func TestPageNames(t *testing.T) {
	a := &Project{Name: "Paxos", Path: "/a/Paxos.pproj"}
	b := &Project{Name: "Paxos", Path: "/b/Paxos.pproj"}
	index := &Project{Name: "index", Path: "/c/index.pproj"}
	s := NewSite("Docs", []*Project{a, b, index}, false)
	if s.Page(a) != "Paxos.adoc" || s.Page(b) != "Paxos-2.adoc" || s.Page(index) != "index-2.adoc" {
		t.Errorf("pages %s, %s, %s", s.Page(a), s.Page(b), s.Page(index))
	}
}

func TestSynopsis(t *testing.T) {
	for overview, want := range map[string]string{
		"":                        "",
		"A bank. It has clients.": "A bank.",
		":toc:\n// note\n\nA bank\nwith clients.\n\nMore.": "A bank with clients.",
		"[NOTE]\n.Title\nFirst line":                       "First line",
	} {
		if got := Synopsis(overview); got != want {
			t.Errorf("Synopsis(%q) = %q, want %q", overview, got, want)
		}
	}
}

var internalLink = regexp.MustCompile(`<<([^,>]+)`)

// checkAnchors checks that every internal link on page has its anchor.
func checkAnchors(t *testing.T, page string) {
	t.Helper()
	for _, m := range internalLink.FindAllStringSubmatch(page, -1) {
		if !strings.Contains(page, "[["+m[1]+"]]\n") {
			t.Errorf("no anchor for <<%s", m[1])
		}
	}
}
