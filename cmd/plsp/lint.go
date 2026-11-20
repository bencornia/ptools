package main

import (
	"cmp"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bencornia/ptools/internal/analysis"
	"github.com/bencornia/ptools/internal/project"
	"github.com/bencornia/ptools/internal/style"
	protocol "github.com/tliron/glsp/protocol_3_16"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// lint checks the P files under paths, as the editor does: their naming,
// and the other checks a .plint.toml turns on. It prints each problem as
// file:line:column: message (code), or with -json, as a JSON array of
// diagnostics. It returns the exit status: 0 for none, 1 for problems, 2 if
// something couldn't be checked.
func lint(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("plsp lint", flag.ContinueOnError)
	flags.SetOutput(stderr)
	asJSON := flags.Bool("json", false, "print the problems as a JSON array of LSP diagnostics, each with its file")
	flags.Usage = func() {
		fmt.Fprintf(stderr, "usage: plsp lint [-json] [path ...]\n\n"+
			"Checks P files for names that don't follow the naming style, unused\n"+
			"declarations, events that are never sent, unreachable states and code, and\n"+
			"shadowed machine variables, as configured by the nearest %s above each\n"+
			"file. Directories are checked recursively, and with no path, the current one.\n\n", style.FileName)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	paths := flags.Args()
	if len(paths) == 0 {
		paths = []string{"."}
	}

	status := 0
	failed := func(err error) {
		fmt.Fprintln(stderr, err)
		status = 2
	}
	l := newLinter()
	defer l.close()
	// A library like the P examples' Common is listed by the projects that
	// include it, which searching up from it doesn't find.
	for _, path := range paths {
		if info, err := os.Stat(strings.TrimSuffix(path, "...") + "."); err == nil && info.IsDir() {
			_, pprojs, _ := project.Find(strings.TrimSuffix(path, "...") + ".")
			l.loadProjects(pprojs)
		}
	}
	problems := []problem{}
	for _, file := range pFiles(paths, failed) {
		diagnostics, err := l.check(file)
		if err != nil {
			failed(err)
			continue
		}
		for _, d := range diagnostics {
			problems = append(problems, problem{File: file, Diagnostic: d})
			status = max(status, 1)
		}
	}

	if *asJSON {
		out, err := json.Marshal(problems)
		if err != nil {
			failed(err)
			return status
		}
		fmt.Fprintf(stdout, "%s\n", out)
		return status
	}
	for _, p := range problems {
		start := p.Range.Start
		fmt.Fprintf(stdout, "%s:%d:%d: %s (%v)\n", p.File, start.Line+1, start.Character+1, p.Message, p.Code.Value)
	}
	return status
}

// problem is a diagnostic in a file, as -json prints it.
type problem struct {
	File string `json:"file"`
	protocol.Diagnostic
}

// linter checks files against the documents compiled with them, parsing
// each file once.
type linter struct {
	parser *sitter.Parser
	docs   map[string]*analysis.Document
	// projects holds every project found so far.
	projects *project.Index
	// rootFiles caches the P files under each fallback root.
	rootFiles map[string][]string
	// configs caches each .plint.toml, by path; "" is the default.
	configs map[string]style.Config
}

func newLinter() *linter {
	return &linter{
		parser:    analysis.NewParser(),
		docs:      map[string]*analysis.Document{},
		projects:  project.NewIndex(),
		rootFiles: map[string][]string{},
		configs:   map[string]style.Config{},
	}
}

func (l *linter) close() {
	for _, doc := range l.docs {
		doc.Close()
	}
	l.parser.Close()
}

// check returns the problems in file, sorted by position.
func (l *linter) check(file string) ([]protocol.Diagnostic, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	config, err := l.config(abs)
	if err != nil {
		return nil, err
	}
	doc, err := l.load(abs)
	if err != nil {
		return nil, err
	}
	diagnostics := analysis.NamingDiagnostics(doc, config.Naming)
	diagnostics = append(diagnostics, analysis.Checks(l.users(abs), doc, config.Checks)...)
	slices.SortStableFunc(diagnostics, func(a, b protocol.Diagnostic) int {
		return cmp.Or(
			cmp.Compare(a.Range.Start.Line, b.Range.Start.Line),
			cmp.Compare(a.Range.Start.Character, b.Range.Start.Character),
		)
	})
	return diagnostics, nil
}

// load parses the file at path, once.
func (l *linter) load(path string) (*analysis.Document, error) {
	if doc, ok := l.docs[path]; ok {
		return doc, nil
	}
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc := analysis.Parse(l.parser, path, 0, string(text))
	l.docs[path] = doc
	return doc, nil
}

// loadProjects adds the .pproj files at paths to the projects files are
// checked with.
func (l *linter) loadProjects(paths []string) {
	for _, p := range paths {
		if abs, err := filepath.Abs(p); err == nil {
			l.projects.Load(abs)
		}
	}
}

// users returns the documents that can use what the file at path
// declares, as the server finds them: those of its projects and of the
// projects including them, or the files under its project.FallbackRoot.
// Files that can't be read are left out.
func (l *linter) users(path string) []*analysis.Document {
	paths, ok := l.projects.Users(path)
	if !ok {
		l.loadProjects(project.Discover(path))
		paths, ok = l.projects.Users(path)
	}
	if !ok {
		root := project.FallbackRoot(path)
		files, found := l.rootFiles[root]
		if !found {
			files, _, _ = project.Find(root)
			l.rootFiles[root] = files
		}
		paths = files
	}
	docs := []*analysis.Document{}
	for _, p := range paths {
		if doc, err := l.load(p); err == nil {
			docs = append(docs, doc)
		}
	}
	return docs
}

// config returns the configuration for the file at path, reading each
// .plint.toml once.
func (l *linter) config(path string) (style.Config, error) {
	file := style.Find(filepath.Dir(path))
	if c, ok := l.configs[file]; ok {
		return c, nil
	}
	c := style.Default()
	if file != "" {
		var err error
		if c, err = style.Load(file); err != nil {
			return style.Config{}, err
		}
	}
	l.configs[file] = c
	return c, nil
}

// pFiles returns the P files among paths and under the directories among
// them, skipping hidden directories. A Go-style dir/... means dir.
func pFiles(paths []string, failed func(error)) []string {
	var files []string
	for _, path := range paths {
		if trimmed := strings.TrimSuffix(path, "..."); trimmed != path {
			path = filepath.Clean(trimmed + ".")
		}
		info, err := os.Stat(path)
		if err != nil {
			failed(err)
			continue
		}
		if !info.IsDir() {
			files = append(files, path)
			continue
		}
		err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && p != path && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".p") {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			failed(err)
		}
	}
	return files
}
