// Package cli runs the ptools commands with fang, which styles their help
// and errors on top of cobra, and keeps their exit statuses: 2 for a usage
// error, 1 for a command that failed, and whatever a command asks for with
// Exit.
package cli

import (
	"context"
	"errors"
	"io"
	"strconv"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"
)

// Run executes root with args, reading stdin and writing stdout and stderr,
// and returns the exit status.
func Run(root *cobra.Command, version string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	// Print the bare version, as the tools always have, for scripts.
	root.SetVersionTemplate("{{.Version}}\n")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	err := fang.Execute(context.Background(), root,
		fang.WithVersion(version),
		fang.WithErrorHandler(func(w io.Writer, styles fang.Styles, err error) {
			var status exitStatus
			if errors.As(err, &status) {
				// The command has already said what went wrong.
				return
			}
			fang.DefaultErrorHandler(w, styles, err)
		}),
	)
	var status exitStatus
	var usage usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &status):
		return int(status)
	case errors.As(err, &usage):
		return 2
	}
	return 1
}

// Exit returns an error that makes Run return status without printing
// anything, for a command that has reported its own problems, or nil for
// status 0.
func Exit(status int) error {
	if status == 0 {
		return nil
	}
	return exitStatus(status)
}

type exitStatus int

func (s exitStatus) Error() string { return "exit status " + strconv.Itoa(int(s)) }

// Args wraps a cobra argument check so that a wrong number of arguments is
// a usage error.
func Args(check cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := check(cmd, args); err != nil {
			return usageError{err}
		}
		return nil
	}
}

// Usage marks err as a mistake in how the command was run, for which Run
// returns status 2.
func Usage(err error) error { return usageError{err} }

// usageError is a mistake in how a command was run. Its text is the
// underlying error's, which fang recognizes to suggest --help.
type usageError struct{ err error }

func (u usageError) Error() string { return u.err.Error() }
func (u usageError) Unwrap() error { return u.err }
