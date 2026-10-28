package main

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bencornia/p-lsp/internal/analysis"
	"github.com/bencornia/p-lsp/internal/style"
)

// lint checks the naming of the P files under paths, as the editor does,
// and prints each problem as file:line:column: message. It returns the exit
// status: 0 for none, 1 for problems, 2 if something couldn't be checked.
func lint(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("p-lsp lint", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintf(stderr, "usage: p-lsp lint [path ...]\n\n"+
			"Checks the names declared in P files against the naming style: the nearest\n"+
			"%s above each file, or the default. Directories are checked recursively,\n"+
			"and with no path, the current one.\n", style.FileName)
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
	parser := analysis.NewParser()
	defer parser.Close()
	styles := map[string]style.Naming{}
	for _, file := range pFiles(paths, failed) {
		text, err := os.ReadFile(file)
		if err != nil {
			failed(err)
			continue
		}
		naming, err := namingFor(file, styles)
		if err != nil {
			failed(err)
			continue
		}
		doc := analysis.Parse(parser, file, 0, string(text))
		for _, p := range analysis.Naming(doc, naming) {
			start := p.Diagnostic.Range.Start
			fmt.Fprintf(stdout, "%s:%d:%d: %s\n", file, start.Line+1, start.Character+1, p.Diagnostic.Message)
			status = max(status, 1)
		}
		doc.Close()
	}
	return status
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

// namingFor returns the naming style for file, reading each directory's
// .plint.toml once.
func namingFor(file string, styles map[string]style.Naming) (style.Naming, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return style.Naming{}, err
	}
	config := style.Find(filepath.Dir(abs))
	if naming, ok := styles[config]; ok {
		return naming, nil
	}
	c := style.Default()
	if config != "" {
		if c, err = style.Load(config); err != nil {
			return style.Naming{}, err
		}
	}
	styles[config] = c.Naming
	return c.Naming, nil
}
