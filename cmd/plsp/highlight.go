package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/bencornia/p-lsp/internal/analysis"
)

// highlight prints the tokens of a P file, or of standard input with no
// file, classified as the editor's semantic highlighting classifies them,
// for highlighting P outside an editor. It prints a JSON array of
// {"start", "end", "type", "declaration"} objects, in document order, with
// byte offsets. Names that don't resolve, and punctuation, are left out.
// It returns the exit status: 0, or 2 if the input couldn't be read.
func highlight(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("p-lsp highlight", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintf(stderr, "usage: p-lsp highlight [file]\n\n"+
			"Prints the tokens of a P file, or of standard input, as a JSON array of\n"+
			"{\"start\", \"end\", \"type\", \"declaration\"} objects with byte offsets,\n"+
			"classified as the editor classifies them. A file's names resolve across\n"+
			"its project; standard input's resolve within itself.\n")
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		flags.Usage()
		return 2
	}

	l := newLinter()
	defer l.close()

	var doc *analysis.Document
	var workspace []*analysis.Document
	if flags.NArg() == 1 {
		abs, err := filepath.Abs(flags.Arg(0))
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		if doc, err = l.load(abs); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		workspace = l.users(abs)
	} else {
		text, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		doc = analysis.Parse(l.parser, "file:///stdin.p", 0, string(text))
		l.docs["stdin"] = doc // closed with the linter
		workspace = []*analysis.Document{doc}
	}

	out, err := json.Marshal(analysis.Highlight(workspace, doc))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fmt.Fprintf(stdout, "%s\n", out)
	return 0
}
