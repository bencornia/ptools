// Command prdb is a replay debugger for P. It steps through a schedule
// PChecker explored, such as one that found a bug, against the program's
// source.
//
// By default it speaks the Debug Adapter Protocol over stdin and stdout,
// for editors. prdb list prints each step of a trace with where in the
// source it happened, and prdb diagram draws a trace as an SVG space-time
// diagram.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bencornia/ptools/internal/cli"
	"github.com/bencornia/ptools/internal/dap"
	"github.com/bencornia/ptools/internal/diagram"
	"github.com/bencornia/ptools/internal/session"
	"github.com/spf13/cobra"
)

const version = "0.1.0"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run runs prdb with args and returns the exit status.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return cli.Run(rootCommand(), version, args, stdin, stdout, stderr)
}

func rootCommand() *cobra.Command {
	var verbose bool
	root := &cobra.Command{
		Use:   "prdb",
		Short: "A replay debugger for P",
		Long: "prdb is a replay debugger for P. It steps through a schedule PChecker\n" +
			"explored, such as one that found a bug, against the program's source. It\n" +
			"speaks the Debug Adapter Protocol over standard input and output, for\n" +
			"editors.",
		Args: cli.Args(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var log io.Writer
			if verbose {
				log = cmd.ErrOrStderr()
			}
			return dap.Serve(cmd.InOrStdin(), cmd.OutOrStdout(), log)
		},
	}
	root.Flags().BoolVarP(&verbose, "verbose", "v", false, "log the protocol messages to stderr")
	root.AddCommand(listCommand(), diagramCommand())
	return root
}

// traceFlags adds the flags that pick the trace to list or draw.
func traceFlags(cmd *cobra.Command, opts *session.Options) {
	flags := cmd.Flags()
	flags.StringVar(&opts.Project, "project", "", "the P project's directory (default: found from the trace's location)")
	flags.StringVar(&opts.TestCase, "testcase", "", "check this test case until PChecker finds a bug, and use the bug's trace")
	flags.IntVar(&opts.Schedules, "schedules", 1000, "how many schedules --testcase explores for a bug")
	flags.IntVar(&opts.Schedule, "schedule", 0, "which of a --verbose trace's schedules to use, from 1 (default: the last)")
}

// traceArgs accepts a trace or schedule, or none with --testcase.
func traceArgs(opts *session.Options) cobra.PositionalArgs {
	return cli.Args(func(cmd *cobra.Command, args []string) error {
		if (len(args) == 1) == (opts.TestCase != "") {
			return fmt.Errorf("invalid argument: give a trace or schedule, or --testcase, but not both")
		}
		return nil
	})
}

func listCommand() *cobra.Command {
	opts := session.Options{}
	cmd := &cobra.Command{
		Use:   "list [trace.json | file.schedule]",
		Short: "Print each step of a trace, with its source location",
		Long: "Prints each step of a PChecker trace with where in the source it happened.\n" +
			"It takes a .trace.json file, a .schedule file, which it compiles the\n" +
			"project to replay, or --testcase, to check a test case until PChecker\n" +
			"finds a bug.",
		Args: traceArgs(&opts),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Progress = cmd.ErrOrStderr()
			return printSteps(cmd.OutOrStdout(), first(args), opts)
		},
	}
	traceFlags(cmd, &opts)
	return cmd
}

func diagramCommand() *cobra.Command {
	opts := session.Options{}
	var out, machines, steps string
	var monitors bool
	cmd := &cobra.Command{
		Use:   "diagram [trace.json | file.schedule]",
		Short: "Draw a trace as an SVG space-time diagram",
		Long: "Draws a PChecker trace as an SVG space-time diagram: a column for each\n" +
			"machine, time running down, and an arrow for each event from the step that\n" +
			"sent it to the step that dequeued it. It takes a trace as prdb list does.",
		Args: traceArgs(&opts),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Progress = cmd.ErrOrStderr()
			return drawDiagram(cmd.OutOrStdout(), first(args), opts, out, machines, steps, monitors)
		},
	}
	traceFlags(cmd, &opts)
	flags := cmd.Flags()
	flags.StringVarP(&out, "out", "o", "", "the file to write (default: standard output)")
	flags.StringVar(&machines, "machines", "", "the machines to draw, by name or type, separated by commas (default: all)")
	flags.StringVar(&steps, "steps", "", "the steps to draw, as first-last, first- or -last (default: all)")
	flags.BoolVar(&monitors, "monitors", true, "draw the spec monitors")
	return cmd
}

func first(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func printSteps(w io.Writer, tracePath string, opts session.Options) error {
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
		fmt.Fprintf(w, "%3d  %-22s %-24s %s\n", i, where, step.Machine, strings.TrimSuffix(step.Log, "."))
	}
	return nil
}

func drawDiagram(stdout io.Writer, tracePath string, opts session.Options, out, machines, steps string, monitors bool) error {
	d := diagram.Options{HideMonitors: !monitors}
	if machines != "" {
		d.Machines = strings.Split(machines, ",")
	}
	if steps != "" {
		first, last, ok := strings.Cut(steps, "-")
		var err1, err2 error
		if first != "" {
			d.From, err1 = strconv.Atoi(first)
		}
		if last != "" {
			d.To, err2 = strconv.Atoi(last)
		}
		if !ok || err1 != nil || err2 != nil {
			return cli.Usage(fmt.Errorf("invalid argument %q for --steps: want first-last, first- or -last", steps))
		}
	}
	s, closeSession, err := session.Open(tracePath, opts)
	if err != nil {
		return err
	}
	defer closeSession()
	d.Title = filepath.Base(tracePath)
	if opts.TestCase != "" {
		d.Title = "The bug in " + opts.TestCase
	}

	w := stdout
	if out != "" {
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	return diagram.SVG(w, s, d)
}
