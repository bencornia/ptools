// Command p-lsp is a language server for the P programming language.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bencornia/p-lsp/internal/server"
	"github.com/tliron/commonlog"
	_ "github.com/tliron/commonlog/simple"
)

const version = "0.1.0"

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	verbosity := flag.Int("v", 0, "log verbosity (logs go to stderr)")
	// Editors commonly pass --stdio; it's the only transport, so accept and ignore it.
	flag.Bool("stdio", true, "communicate over stdin/stdout")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	commonlog.Configure(*verbosity, nil)

	if err := server.New(version).RunStdio(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
