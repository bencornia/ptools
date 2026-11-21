// Command prdb is a replay debugger for P. It steps through a schedule
// PChecker explored, such as one that found a bug, against the program's
// source.
//
// By default it speaks the Debug Adapter Protocol over stdin and stdout,
// for editors. prdb list prints each step of a trace with where in the
// source it happened, prdb diagram draws a trace as an SVG space-time
// diagram, and prdb check checks a test case until PChecker finds a bug.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bencornia/ptools/internal/cli"
	"github.com/bencornia/ptools/internal/dap"
	"github.com/bencornia/ptools/internal/diagram"
	"github.com/bencornia/ptools/internal/replay"
	"github.com/bencornia/ptools/internal/session"
	"github.com/spf13/cobra"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run runs prdb with args and returns the exit status.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return cli.Run(rootCommand(), args, stdin, stdout, stderr)
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
	root.AddCommand(listCommand(), diagramCommand(), checkCommand())
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
			return hint(printSteps(cmd.OutOrStdout(), first(args), opts))
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
			return hint(drawDiagram(cmd.OutOrStdout(), first(args), opts, out, machines, steps, monitors))
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

func checkCommand() *cobra.Command {
	var project string
	var schedules int
	var compiler []string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "check testcase",
		Short: "Check a test case with PChecker until it finds a bug",
		Long: "Compiles the P project and checks the test case with PChecker, exploring up\n" +
			"to --schedules schedules, until it finds a bug. A parameterised test can be\n" +
			"named for all its cases, which are checked in turn. The P tool's output goes\n" +
			"to standard error.\n\n" +
			"If PChecker finds a bug, check saves its trace and schedule in\n" +
			"PCheckerOutput/prdb, prints the trace's path, and exits with status 1;\n" +
			"prdb list and diagram, and the debugger, take the trace. Otherwise it exits\n" +
			"with status 0.",
		Args: cli.Args(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if project == "" {
				project = session.FindProject(".")
			}
			stderr := cmd.ErrOrStderr()
			bug, err := replay.Check(args[0], project, schedules, compiler, stderr, stderr)
			if err != nil {
				return hint(err)
			}
			stdout := cmd.OutOrStdout()
			if bug == nil {
				fmt.Fprintf(stderr, "PChecker found no bug in %s in %d schedules\n", args[0], schedules)
			}
			switch {
			case asJSON:
				out, err := json.Marshal(struct {
					Bug *replay.Bug `json:"bug"`
				}{bug})
				if err != nil {
					return err
				}
				fmt.Fprintf(stdout, "%s\n", out)
			case bug != nil:
				fmt.Fprintln(stdout, bug.Trace)
			}
			if bug != nil {
				return cli.Exit(1)
			}
			return nil
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&project, "project", "", "the P project's directory (default: the nearest, from the current directory up, with a .pproj)")
	flags.IntVar(&schedules, "schedules", 1000, "how many schedules to explore")
	flags.StringArrayVar(&compiler, "compiler", []string{"p"}, "the command that runs the P tool; repeat it for each of the command's arguments")
	flags.BoolVar(&asJSON, "json", false, `print {"bug": {"test", "trace"}}, or {"bug": null}, instead of the trace's path`)
	return cmd
}

// hint adds how to set the P tool's command to an error saying it can't
// be found.
func hint(err error) error {
	if errors.Is(err, replay.ErrNoTool) {
		return fmt.Errorf("%w; pass --compiler with the command that runs it", err)
	}
	return err
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
