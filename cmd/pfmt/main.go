// Command pfmt formats P source code.
//
// With no files, it formats standard input to standard output. Given files
// or directories (searched for .p files), it prints the formatted files, or
// with -w writes them back, with -l lists the files whose formatting
// differs, or with -d prints diffs of what formatting would change.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bencornia/ptools/internal/cli"
	"github.com/bencornia/ptools/internal/diff"
	"github.com/bencornia/ptools/internal/format"
	"github.com/spf13/cobra"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run runs pfmt with args and returns the exit status.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return cli.Run(rootCommand(), args, stdin, stdout, stderr)
}

// modes says what to do with formatted files.
type modes struct {
	write, list, diff bool
}

func rootCommand() *cobra.Command {
	var m modes
	var indent int
	root := &cobra.Command{
		Use:   "pfmt [path ...]",
		Short: "Format P source code",
		Long: "pfmt formats P source code. With no paths, it formats standard input to\n" +
			"standard output. Given files, or directories, which it searches for .p\n" +
			"files, it prints the formatted files, or with -w writes them back.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := format.Options{Indent: indent}
			stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
			if len(args) == 0 {
				if m.write || m.list {
					return cli.Usage(errors.New("invalid argument: -w and -l need files to work on"))
				}
				src, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				out, err := format.Source(src, opts)
				if err != nil {
					fmt.Fprintf(stderr, "<stdin>: %v\n", err)
					return cli.Exit(2)
				}
				if m.diff {
					stdout.Write(diff.Unified("<stdin>.orig", "<stdin>", src, out))
				} else {
					stdout.Write(out)
				}
				return nil
			}

			status := 0
			for _, path := range files(args) {
				if err := formatFile(stdout, path, opts, m); err != nil {
					fmt.Fprintf(stderr, "%s: %v\n", path, err)
					status = 1
				}
			}
			return cli.Exit(status)
		},
	}
	flags := root.Flags()
	flags.BoolVarP(&m.write, "write", "w", false, "write the result to the files instead of printing it")
	flags.BoolVarP(&m.list, "list", "l", false, "list the files whose formatting differs")
	flags.BoolVarP(&m.diff, "diff", "d", false, "print diffs of what formatting would change instead of formatted files")
	flags.IntVar(&indent, "indent", 4, "spaces per indentation level")
	return root
}

// files expands the arguments into files, with the .p files in each
// directory, skipping hidden ones.
func files(args []string) []string {
	var paths []string
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil || !info.IsDir() {
			paths = append(paths, arg)
			continue
		}
		filepath.WalkDir(arg, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			hidden := path != arg && strings.HasPrefix(d.Name(), ".")
			switch {
			case d.IsDir() && hidden:
				return filepath.SkipDir
			case !d.IsDir() && !hidden && strings.HasSuffix(path, ".p"):
				paths = append(paths, path)
			}
			return nil
		})
	}
	return paths
}

func formatFile(stdout io.Writer, path string, opts format.Options, m modes) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := format.Source(src, opts)
	if err != nil {
		return err
	}
	changed := !bytes.Equal(src, out)
	if m.list && changed {
		fmt.Fprintln(stdout, path)
	}
	if m.diff {
		stdout.Write(diff.Unified(path+".orig", path, src, out))
	}
	if m.write && changed {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		return os.WriteFile(path, out, info.Mode().Perm())
	}
	if !m.list && !m.diff && !m.write {
		stdout.Write(out)
	}
	return nil
}
