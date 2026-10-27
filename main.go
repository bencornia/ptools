// Command pfmt formats P source code.
//
// With no files, it formats standard input to standard output. Given files
// or directories (searched for .p files), it prints the formatted files, or
// with -w writes them back, or with -l lists the files whose formatting
// differs.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bencornia/pfmt/format"
)

func main() {
	write := flag.Bool("w", false, "write the result to the files instead of printing it")
	list := flag.Bool("l", false, "list the files whose formatting differs")
	indent := flag.Int("indent", 4, "spaces per indentation level")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: pfmt [flags] [path ...]\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	opts := format.Options{Indent: *indent}

	if flag.NArg() == 0 {
		if *write || *list {
			fail("-w and -l need files to work on")
		}
		src, err := io.ReadAll(os.Stdin)
		if err != nil {
			fail(err.Error())
		}
		out, err := format.Source(src, opts)
		if err != nil {
			fail("<stdin>: " + err.Error())
		}
		os.Stdout.Write(out)
		return
	}

	failed := false
	for _, path := range files(flag.Args()) {
		if err := formatFile(path, opts, *write, *list); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
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

func formatFile(path string, opts format.Options, write, list bool) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := format.Source(src, opts)
	if err != nil {
		return err
	}
	changed := !bytes.Equal(src, out)
	switch {
	case list:
		if changed {
			fmt.Println(path)
		}
	case write:
		if changed {
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			return os.WriteFile(path, out, info.Mode().Perm())
		}
	default:
		os.Stdout.Write(out)
	}
	return nil
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "pfmt: "+message)
	os.Exit(2)
}
