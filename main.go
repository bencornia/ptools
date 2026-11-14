// Command p-lsp is a language server for the P programming language.
//
// Run as p-lsp lint [-json] [path ...], it checks P files for lint problems
// instead: naming, unused declarations, and the other checks .plint.toml
// configures. Run as p-lsp doc [-o dir] [-private] [path ...], it writes
// AsciiDoc reference documentation for P projects. Run as p-lsp highlight
// [file], it prints a file's semantic tokens as JSON, for highlighting P
// outside an editor.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bencornia/p-lsp/internal/server"
	"github.com/tliron/commonlog"
	_ "github.com/tliron/commonlog/simple"
)

const version = "0.6.0"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "lint" {
		os.Exit(lint(os.Args[2:], os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "highlight" {
		os.Exit(highlight(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "doc" {
		os.Exit(docs(os.Args[2:], os.Stdout, os.Stderr))
	}

	showVersion := flag.Bool("version", false, "print the version and exit")
	verbosity := flag.Int("v", 0, "log verbosity (logs go to stderr)")
	// Editors commonly pass --stdio; it's the only transport, so accept and ignore it.
	flag.Bool("stdio", true, "communicate over stdin/stdout")
	compiler := flag.String("compiler", "p", "command that runs the P compiler, for type-check diagnostics on save; empty turns them off")
	compileTimeout := flag.Duration("compile-timeout", time.Minute, "how long a type check may take")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	commonlog.Configure(*verbosity, nil)

	s := server.New(version)
	if command := strings.Fields(*compiler); len(command) > 0 {
		s.SetCompiler(command, *compileTimeout)
	}
	if err := s.RunStdio(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
