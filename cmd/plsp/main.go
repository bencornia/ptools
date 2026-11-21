// Command plsp is a language server for the P programming language.
//
// Run as plsp lint [--json] [path ...], it checks P files for lint problems
// instead: naming, unused declarations, and the other checks .plint.toml
// configures. Run as plsp highlight [file], it prints a file's semantic
// tokens as JSON, for highlighting P outside an editor. Run as plsp doctor
// [--fix], it checks that ptools, the P compiler and the user's editors are
// set up, and fixes what it can.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bencornia/ptools/internal/cli"
	"github.com/bencornia/ptools/internal/server"
	"github.com/bencornia/ptools/internal/version"
	"github.com/spf13/cobra"
	"github.com/tliron/commonlog"
	_ "github.com/tliron/commonlog/simple"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run runs plsp with args and returns the exit status.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return cli.Run(rootCommand(), args, stdin, stdout, stderr)
}

func rootCommand() *cobra.Command {
	var (
		verbosity      int
		compiler       string
		compileTimeout time.Duration
	)
	root := &cobra.Command{
		Use:   "plsp",
		Short: "A language server for the P programming language",
		Long: "plsp is a language server for the P programming language. It speaks the\n" +
			"Language Server Protocol over standard input and output, for editors.",
		Args: cli.Args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			commonlog.Configure(verbosity, nil)
			s := server.New(version.Version)
			if command := strings.Fields(compiler); len(command) > 0 {
				s.SetCompiler(command, compileTimeout)
			}
			if err := s.RunStdio(); err != nil {
				return fmt.Errorf("serving: %w", err)
			}
			return nil
		},
	}
	flags := root.Flags()
	flags.IntVarP(&verbosity, "verbosity", "v", 0, "log verbosity (logs go to stderr)")
	// Editors commonly pass --stdio; it's the only transport, so accept and ignore it.
	flags.Bool("stdio", true, "communicate over stdin/stdout")
	flags.StringVar(&compiler, "compiler", "p", "command that runs the P compiler, for type-check diagnostics on save; empty turns them off")
	flags.DurationVar(&compileTimeout, "compile-timeout", time.Minute, "how long a type check may take")
	root.AddCommand(lintCommand(), highlightCommand(), doctorCommand())
	return root
}
