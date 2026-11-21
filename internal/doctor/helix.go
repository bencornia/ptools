package doctor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

// helixLanguages is what the doctor reads of Helix's languages.toml.
type helixLanguages struct {
	LanguageServer map[string]struct {
		Command string
	} `toml:"language-server"`
	Language []struct {
		Name            string
		LanguageServers []any `toml:"language-servers"`
	}
	Grammar []struct {
		Name   string
		Source struct {
			Git, Rev string
		}
	}
}

// helix checks Helix's configuration for P, and its build of the grammar,
// which Helix highlights and indents with.
func (d *Doctor) helix() {
	_, hx := exec.LookPath("hx")
	_, helix := exec.LookPath("helix") // as some distributions name it
	if hx != nil && helix != nil {
		d.skip("Helix", "not installed")
		return
	}
	dir := helixConfigDir()
	d.section("Helix")
	if d.helixConfig(filepath.Join(dir, "languages.toml")) {
		d.helixGrammar(filepath.Join(dir, "runtime"))
	}
}

// helixConfigDir is where Helix reads its configuration from.
func helixConfigDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("APPDATA"), "helix")
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "helix")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "helix")
}

// helixConfig checks that languages.toml sets up P with plsp and pins the
// grammar to d.GrammarRev, adding what's missing to fix it. It reports
// whether the grammar is the doctor's to build.
func (d *Doctor) helixConfig(file string) bool {
	text, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		d.bad(false, "can't read %s: %v", tilde(file), err)
		return false
	}
	var config helixLanguages
	if _, err := toml.Decode(string(text), &config); err != nil {
		d.bad(false, "can't read %s: %v", tilde(file), err)
		return false
	}
	name := filepath.Base(file)
	var add []string // blocks to append
	var fixes []string
	edited := string(text)

	server := ""
	for name, s := range config.LanguageServer {
		if filepath.Base(s.Command) == "plsp" {
			server = name
		}
	}
	language := -1
	for i, l := range config.Language {
		if l.Name == "p" {
			language = i
		}
	}
	switch {
	case language < 0:
		if d.bad(true, "%s doesn't set up P", name) {
			if server == "" {
				server = "plsp"
				if _, ok := config.LanguageServer[server]; ok {
					d.failed(fmt.Errorf("%s has a language server named plsp that doesn't run plsp", name))
					return false
				}
				add = append(add, helixServer)
			}
			add = append(add, fmt.Sprintf(helixLanguage, server))
			fixes = append(fixes, "added P, with plsp")
		}
	case server == "" || !usesServer(config.Language[language].LanguageServers, server):
		d.bad(false, `%s's P language doesn't use plsp. Add plsp as a language server, and to the language's language-servers`, name)
	default:
		d.good("%s sets up P with plsp", name)
	}

	grammar := -1
	for i, g := range config.Grammar {
		if g.Name == "p" {
			grammar = i
		}
	}
	short := d.GrammarRev[:7]
	pin := fmt.Sprintf(helixGrammar, d.GrammarRepo, d.GrammarRev)
	switch {
	case grammar < 0:
		if language < 0 {
			// Setting up P adds it.
			if d.Fix {
				add = append(add, pin)
			}
		} else if d.bad(true, "%s doesn't say where to get P's grammar", name) {
			add = append(add, pin)
			fixes = append(fixes, "added P's grammar")
		}
	case !sameRepo(config.Grammar[grammar].Source.Git, d.GrammarRepo):
		d.note("%s builds P's grammar from %s, so the doctor leaves the grammar to you", name, config.Grammar[grammar].Source.Git)
		return false
	case config.Grammar[grammar].Source.Rev != d.GrammarRev:
		old := config.Grammar[grammar].Source.Rev
		if d.bad(true, "%s pins P's grammar to %s, not %s, the one plsp parses with", name, shortRev(old), short) {
			if old == "" || strings.Count(edited, old) != 1 {
				d.failed(fmt.Errorf("can't find where to change it; set the P grammar's rev to %s", d.GrammarRev))
				return false
			}
			edited = strings.Replace(edited, old, d.GrammarRev, 1)
			fixes = append(fixes, "pinned P's grammar to "+short)
		}
	default:
		d.good("%s pins P's grammar to %s", name, short)
	}

	if edited == string(text) && len(add) == 0 {
		return true
	}
	if len(add) > 0 {
		if edited != "" && !strings.HasSuffix(edited, "\n") {
			edited += "\n"
		}
		if edited != "" {
			edited += "\n"
		}
		edited += helixComment + strings.Join(add, "\n")
	}
	if err := writeConfig(file, text, edited); err != nil {
		d.failed(err)
		return false
	}
	d.doing("changed %s: %s", tilde(file), strings.Join(fixes, ", and "))
	if len(text) > 0 {
		d.doing("kept the old one as %s", tilde(file+".bak"))
	}
	return true
}

const (
	helixComment  = "# P, with plsp from ptools (https://github.com/bencornia/ptools), as plsp\n# doctor set it up.\n"
	helixServer   = "[language-server.plsp]\ncommand = \"plsp\"\n"
	helixLanguage = `[[language]]
name = "p"
scope = "source.p"
file-types = ["p"]
comment-token = "//"
block-comment-tokens = { start = "/*", end = "*/" }
indent = { tab-width = 4, unit = "    " }
language-servers = [%q]
# Format with pfmt, through the server, on save.
auto-format = true
`
	helixGrammar = `[[grammar]]
name = "p"
source = { git = %q, rev = %q }
`
)

// writeConfig replaces file's text, old, with text, keeping old as a
// backup, after checking that Helix can still read it.
func writeConfig(file string, old []byte, text string) error {
	var check helixLanguages
	if _, err := toml.Decode(text, &check); err != nil {
		return fmt.Errorf("changing %s would break it (%v), so it's unchanged", tilde(file), err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	if len(old) > 0 {
		if err := os.WriteFile(file+".bak", old, 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(file, []byte(text), 0o644)
}

func usesServer(servers []any, name string) bool {
	for _, s := range servers {
		switch s := s.(type) {
		case string:
			if s == name {
				return true
			}
		case map[string]any:
			if s["name"] == name {
				return true
			}
		}
	}
	return false
}

func sameRepo(a, b string) bool {
	clean := func(s string) string {
		return strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git")
	}
	return clean(a) == clean(b)
}

func shortRev(rev string) string {
	if rev == "" {
		return "no commit"
	}
	if len(rev) > 7 {
		return rev[:7]
	}
	return rev
}

// helixGrammar checks Helix's build of the grammar and its queries, and
// fetches and builds them as `hx --grammar fetch` and `hx --grammar build`
// would, to fix them. It does that itself because hx fetches every grammar
// unless languages.toml says otherwise.
func (d *Doctor) helixGrammar(dir string) {
	source := filepath.Join(dir, "grammars", "sources", "p")
	library := filepath.Join(dir, "grammars", "p.so")
	if runtime.GOOS == "windows" {
		library = filepath.Join(dir, "grammars", "p.dll")
	}
	short := d.GrammarRev[:7]
	problem := ""
	head, err := run(nil, "git", "-C", source, "rev-parse", "HEAD")
	if err != nil {
		head = ""
	}
	switch head = strings.TrimSpace(head); {
	case !exists(library):
		problem = "P's grammar isn't built"
	case head != d.GrammarRev:
		problem = fmt.Sprintf("P's grammar is built from %s, not %s", shortRev(head), short)
	case newer(filepath.Join(source, "src", "parser.c"), library):
		problem = "P's grammar wasn't built since it was last fetched"
	}
	switch {
	case problem == "":
		d.good("P's grammar is built, at %s", short)
	case !d.bad(runtime.GOOS != "windows", "%s", problem):
		if runtime.GOOS == "windows" {
			d.note("Build it with hx:")
			d.code("hx --grammar fetch && hx --grammar build")
		}
		return
	default:
		d.doing("fetching tree-sitter-p %s and building it", short)
		if err := d.buildGrammar(source, library); err != nil {
			d.failed(err)
			return
		}
	}

	queries, _ := filepath.Glob(filepath.Join(source, "queries", "*.scm"))
	installed := filepath.Join(dir, "queries", "p")
	var stale []string
	for _, q := range queries {
		want, err := os.ReadFile(q)
		if err != nil {
			continue
		}
		got, err := os.ReadFile(filepath.Join(installed, filepath.Base(q)))
		if err != nil || !bytes.Equal(got, want) {
			stale = append(stale, q)
		}
	}
	switch {
	case len(queries) == 0:
		d.bad(false, "tree-sitter-p, in %s, has no queries", tilde(source))
	case len(stale) == 0:
		d.good("its queries are installed, in %s", tilde(installed))
	case d.bad(true, "%s", staleQueries(len(stale), len(queries))):
		d.doing("copying them to %s", tilde(installed))
		if err := copyFiles(stale, installed); err != nil {
			d.failed(err)
		}
	}
}

func staleQueries(n, of int) string {
	if n == of {
		return "its queries aren't installed, or are out of date"
	}
	return fmt.Sprintf("%d of its %d queries aren't installed, or are out of date", n, of)
}

// buildGrammar fetches the grammar into source and builds library from it,
// with the same commands Helix uses.
func (d *Doctor) buildGrammar(source, library string) error {
	for _, tool := range []string{"git", "cc"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("building the grammar needs %s, which isn't installed", tool)
		}
	}
	if err := os.MkdirAll(source, 0o755); err != nil {
		return err
	}
	git := func(args ...string) error { return runIn(source, nil, "git", args...) }
	if !exists(filepath.Join(source, ".git")) {
		if err := git("init", "-q"); err != nil {
			return err
		}
	}
	remote, err := run(nil, "git", "-C", source, "remote", "get-url", "origin")
	if remote = strings.TrimSpace(remote); err != nil {
		if err := git("remote", "add", "origin", d.GrammarRepo); err != nil {
			return err
		}
	} else if remote != d.GrammarRepo {
		if err := git("remote", "set-url", "origin", d.GrammarRepo); err != nil {
			return err
		}
	}
	if err := git("fetch", "-q", "--depth", "1", "origin", d.GrammarRev); err != nil {
		return err
	}
	if err := git("checkout", "-q", "--force", d.GrammarRev); err != nil {
		return err
	}
	src := filepath.Join(source, "src")
	args := []string{"-shared", "-fPIC", "-fno-exceptions", "-O3", "-I", src, "-o", library, "-xc", "-std=c11"}
	if scanner := filepath.Join(src, "scanner.c"); exists(scanner) {
		args = append(args, scanner)
	}
	args = append(args, filepath.Join(src, "parser.c"))
	if err := os.MkdirAll(filepath.Dir(library), 0o755); err != nil {
		return err
	}
	return runIn(source, nil, "cc", args...)
}

// newer reports whether file a was changed after file b.
func newer(a, b string) bool {
	sa, err := os.Stat(a)
	if err != nil {
		return false
	}
	sb, err := os.Stat(b)
	return err == nil && sa.ModTime().After(sb.ModTime())
}

func copyFiles(files []string, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(f)), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
