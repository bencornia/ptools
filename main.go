// Command prdb is a replay debugger for P. It steps through a schedule
// PChecker explored, such as one that found a bug, against the program's
// source.
//
// By default it speaks the Debug Adapter Protocol over stdin and stdout,
// for editors. With -list, it prints each step of a trace with where in
// the source it happened.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bencornia/prdb/internal/dap"
	"github.com/bencornia/prdb/internal/session"
)

const version = "0.1.0"

func main() {
	list := flag.Bool("list", false, "print each step of the trace or schedule given as an argument, with its source location")
	project := flag.String("project", "", "the P project's directory (default: found from the trace's location)")
	testCase := flag.String("testcase", "", "with -list, check this test case until PChecker finds a bug, and list the bug's trace")
	schedules := flag.Int("schedules", 1000, "how many schedules -testcase explores for a bug")
	schedule := flag.Int("schedule", 0, "which of a --verbose trace's schedules to list, from 1 (default: the last)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	verbose := flag.Bool("v", false, "log the protocol messages to stderr")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: prdb [flags]               (debug adapter, over stdio)\n       prdb -list [flags] trace.json|file.schedule\n       prdb -list -testcase name [flags]\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	switch {
	case *showVersion:
		fmt.Println(version)
	case *list:
		if (flag.NArg() == 1) == (*testCase != "") {
			flag.Usage()
			os.Exit(2)
		}
		opts := session.Options{Project: *project, Progress: os.Stderr, TestCase: *testCase, Schedules: *schedules, Schedule: *schedule}
		if err := printSteps(flag.Arg(0), opts); err != nil {
			fmt.Fprintln(os.Stderr, "prdb:", err)
			os.Exit(1)
		}
	default:
		var log *os.File
		if *verbose {
			log = os.Stderr
		}
		if err := dap.Serve(os.Stdin, os.Stdout, log); err != nil {
			fmt.Fprintln(os.Stderr, "prdb:", err)
			os.Exit(1)
		}
	}
}

func printSteps(tracePath string, opts session.Options) error {
	s, closeSession, err := session.Open(tracePath, opts)
	if err != nil {
		return err
	}
	defer closeSession()
	for i, step := range s.Steps {
		where := "?"
		if w := s.Where[i]; w.File != "" {
			rel, err := filepath.Rel(s.Source.Root, w.File)
			if err != nil {
				rel = w.File
			}
			where = fmt.Sprintf("%s:%d", filepath.ToSlash(rel), w.Line)
		}
		fmt.Printf("%3d  %-22s %-24s %s\n", i, where, step.Machine, strings.TrimSuffix(step.Log, "."))
	}
	return nil
}
