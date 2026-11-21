// Command pdoc writes reference documentation for P projects as AsciiDoc,
// organized like Go's package documentation: a page for each project
// (.pproj file), with its README.adoc as the overview, an index of its
// declarations, and a section for each with its doc comment.
//
// Usage:
//
//	pdoc [-o dir] [--private] [--title title] [path ...]
//
// With -o, it writes a page for each project found under the paths, and an
// index.adoc listing them, to dir; without, it prints the page of the one
// project found.
package main

import (
	"io"
	"os"

	"github.com/bencornia/ptools/internal/cli"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run runs pdoc with args and returns the exit status.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return cli.Run(rootCommand(), args, stdin, stdout, stderr)
}
