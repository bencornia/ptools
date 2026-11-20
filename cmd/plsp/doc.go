package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/bencornia/ptools/internal/analysis"
	"github.com/bencornia/ptools/internal/cli"
	"github.com/bencornia/ptools/internal/doc"
	"github.com/bencornia/ptools/internal/project"
	"github.com/spf13/cobra"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

func docCommand() *cobra.Command {
	var o docOptions
	cmd := &cobra.Command{
		Use:   "doc [path ...]",
		Short: "Write AsciiDoc reference documentation for P projects",
		Long: "Writes AsciiDoc reference documentation for the P projects (.pproj files)\n" +
			"among the paths and under the directories among them, the current one by\n" +
			"default. A project's page opens with the README.adoc beside its .pproj, if\n" +
			"there is one. Without -o, it prints the page of the one project found.\n" +
			"Asciidoctor turns the pages into HTML.",
		RunE: func(cmd *cobra.Command, paths []string) error {
			return cli.Exit(docs(paths, o, cmd.OutOrStdout(), cmd.ErrOrStderr()))
		},
	}
	cmd.Flags().StringVarP(&o.out, "out", "o", "", "write a page for each project, and index.adoc, to `dir`")
	cmd.Flags().BoolVar(&o.private, "private", false, "document machines' variables and functions too")
	cmd.Flags().StringVar(&o.title, "title", "P projects", "the index page's title")
	return cmd
}

type docOptions struct {
	out, title string
	private    bool
}

// docs writes AsciiDoc reference documentation for the P projects (.pproj
// files) among paths and under the directories among them: with an output
// directory, a page for each project and an index page listing them;
// without, the one project's page, printed. It returns the exit status: 0,
// or 2 if something went wrong.
func docs(paths []string, o docOptions, stdout, stderr io.Writer) int {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	pprojs, err := findProjects(paths)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if len(pprojs) == 0 {
		fmt.Fprintf(stderr, "no .pproj files in %s\n", strings.Join(paths, ", "))
		return 2
	}
	if o.out == "" && len(pprojs) > 1 {
		fmt.Fprintf(stderr, "found %d projects; use -o to write a page for each\n", len(pprojs))
		return 2
	}

	l := &docLoader{parser: analysis.NewParser(), docs: map[string]*analysis.Document{}, projects: map[string]*project.Project{}, stderr: stderr}
	defer l.close()
	var projects []*doc.Project
	for _, path := range pprojs {
		p, err := l.project(path)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", path, err)
			return 2
		}
		projects = append(projects, p)
	}
	site := doc.NewSite(o.title, projects, o.private)

	if o.out == "" {
		if err := site.WritePage(stdout, projects[0]); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		return 0
	}
	if err := os.MkdirAll(o.out, 0o755); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	write := func(name string, render func(io.Writer) error) error {
		f, err := os.Create(filepath.Join(o.out, name))
		if err != nil {
			return err
		}
		if err := render(f); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	}
	for _, p := range projects {
		if err := write(site.Page(p), func(w io.Writer) error { return site.WritePage(w, p) }); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}
	if err := write("index.adoc", site.WriteIndex); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	return 0
}

// findProjects returns the .pproj files among paths and under the
// directories among them, each once, by absolute path.
func findProjects(paths []string) ([]string, error) {
	var found []string
	for _, path := range paths {
		path = strings.TrimSuffix(path, "...")
		if path == "" {
			path = "."
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		var pprojs []string
		if info.IsDir() {
			if _, pprojs, err = project.Find(path); err != nil {
				return nil, err
			}
		} else if strings.EqualFold(filepath.Ext(path), ".pproj") {
			pprojs = []string{path}
		} else {
			return nil, fmt.Errorf("%s isn't a .pproj file or a directory", path)
		}
		for _, p := range pprojs {
			if abs, err := filepath.Abs(p); err == nil && !slices.Contains(found, abs) {
				found = append(found, abs)
			}
		}
	}
	return found, nil
}

// docLoader reads projects and parses their files, each once.
type docLoader struct {
	parser   *sitter.Parser
	docs     map[string]*analysis.Document
	projects map[string]*project.Project
	stderr   io.Writer
}

func (l *docLoader) close() {
	for _, d := range l.docs {
		d.Close()
	}
	l.parser.Close()
}

func (l *docLoader) parse(path string) (*project.Project, error) {
	if p, ok := l.projects[path]; ok {
		return p, nil
	}
	p, err := project.Parse(path)
	if err != nil {
		return nil, err
	}
	l.projects[path] = p
	return p, nil
}

func (l *docLoader) load(paths []string) []*analysis.Document {
	var docs []*analysis.Document
	for _, path := range paths {
		d, ok := l.docs[path]
		if !ok {
			text, err := os.ReadFile(path)
			if err != nil {
				fmt.Fprintln(l.stderr, err)
				continue
			}
			d = analysis.Parse(l.parser, path, 0, string(text))
			l.docs[path] = d
		}
		docs = append(docs, d)
	}
	return docs
}

// project documents the project at path, against its files and those of
// the projects it includes.
func (l *docLoader) project(path string) (*doc.Project, error) {
	p, err := l.parse(path)
	if err != nil {
		return nil, err
	}
	workspace := slices.Clone(p.Files)
	var includes []string
	queue := slices.Clone(p.Includes)
	for len(queue) > 0 {
		inc := queue[0]
		queue = queue[1:]
		if inc == p.Path || slices.Contains(includes, inc) {
			continue
		}
		includes = append(includes, inc)
		ip, err := l.parse(inc)
		if err != nil {
			fmt.Fprintf(l.stderr, "%s: included project %s: %v\n", path, inc, err)
			continue
		}
		workspace = append(workspace, ip.Files...)
		queue = append(queue, ip.Includes...)
	}
	dir := filepath.Dir(p.Path)
	return &doc.Project{
		Name:     p.Name,
		Dir:      dir,
		Path:     p.Path,
		Files:    p.Files,
		Includes: includes,
		Package:  analysis.Docs(l.load(p.Files), l.load(workspace)),
		Overview: readme(dir),
	}, nil
}

// documentTitle matches an AsciiDoc document's title and the attribute
// lines after it.
var documentTitle = regexp.MustCompile(`\A\s*= [^\n]*\n(:[^\n]*\n)*`)

// readme returns the README.adoc in dir without its title, which the
// project's page has its own of, or "".
func readme(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if strings.EqualFold(e.Name(), "README.adoc") {
			text, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				return ""
			}
			return documentTitle.ReplaceAllString(string(text), "")
		}
	}
	return ""
}
