package compile

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// ErrCanceled is returned by Check when a newer check of the same project
// replaced it.
var ErrCanceled = errors.New("replaced by a newer check")

// typeCheckedMarker is printed once type checking has passed and code
// generation starts. Everything after it is slow (generating code and, for
// the default backend, building it with .NET) and can't report type errors,
// so a check stops there.
const typeCheckedMarker = "Code generation for"

// Runner type-checks P projects with the P compiler.
type Runner struct {
	// Command runs the compiler, as in []string{"p"}. Check appends the
	// compile arguments.
	Command []string
	// Timeout bounds each check.
	Timeout time.Duration

	mu sync.Mutex
	// running cancels the check in progress for each project.
	running map[string]context.CancelFunc
	// env is the environment to run the compiler in, or nil for plsp's.
	env []string
	// before holds the names in each project's directory before plsp
	// first compiled it.
	before map[string]map[string]bool
}

// Available reports whether the compiler command can be found. Editors
// started from a desktop often lack the PATH and DOTNET_ROOT a shell
// profile sets, so when the command is the default "p" and isn't on the
// PATH, it looks where `dotnet tool install --global` puts it, and from
// then on runs it with the .NET it was installed with.
func (r *Runner) Available() error {
	if len(r.Command) == 0 {
		return errors.New("no compiler command configured")
	}
	_, err := exec.LookPath(r.Command[0])
	if err == nil || r.Command[0] != "p" {
		return err
	}
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		return err
	}
	tool := filepath.Join(home, ".dotnet", "tools", "p")
	if _, statErr := os.Stat(tool); statErr != nil {
		return err
	}
	r.Command = append([]string{tool}, r.Command[1:]...)
	root := os.Getenv("DOTNET_ROOT")
	if root == "" {
		root = filepath.Join(home, ".dotnet")
	}
	if _, statErr := os.Stat(filepath.Join(root, "dotnet")); statErr == nil {
		r.env = append(os.Environ(), "DOTNET_ROOT="+root, "PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return nil
}

// Check type-checks the project at pproj, whose files are files, and returns
// the errors the compiler reports. Starting a check of a project cancels the
// one already running for it, which then returns ErrCanceled.
func (r *Runner) Check(pproj string, files []string) ([]Error, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.Timeout)
	defer cancel()
	r.mu.Lock()
	if r.running == nil {
		r.running = map[string]context.CancelFunc{}
		r.before = map[string]map[string]bool{}
	}
	if r.before[pproj] == nil {
		r.before[pproj] = entries(filepath.Dir(pproj))
	}
	if previous, ok := r.running[pproj]; ok {
		previous()
	}
	replaced, replace := context.WithCancel(ctx)
	r.running[pproj] = replace
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		// A newer check may have taken the slot already, and the cleaning
		// up with it.
		if replaced.Err() == nil {
			delete(r.running, pproj)
			removeNewEmpty(filepath.Dir(pproj), r.before[pproj])
		}
		r.mu.Unlock()
		replace()
	}()

	// Keep generated code out of the user's project.
	outDir, err := os.MkdirTemp("", "plsp-compile-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(outDir)

	args := append(slices.Clone(r.Command[1:]), "compile", "--pproj", pproj, "--outdir", outDir)
	run, stop := context.WithCancel(replaced)
	defer stop()
	cmd := exec.CommandContext(run, r.Command[0], args...)
	cmd.Dir = filepath.Dir(pproj)
	cmd.Env = r.env
	stopTree(cmd)
	// Don't wait long on children still holding the output pipe open once
	// the compiler itself has exited or been stopped.
	cmd.WaitDelay = 2 * time.Second

	reader, writer := io.Pipe()
	cmd.Stdout, cmd.Stderr = writer, writer
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		writer.Close()
		done <- err
	}()

	var output strings.Builder
	typeChecked := false
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		output.WriteString(line + "\n")
		if strings.Contains(line, typeCheckedMarker) && !typeChecked {
			typeChecked = true
			stop()
		}
	}
	io.Copy(io.Discard, reader)
	waitErr := <-done

	switch {
	case typeChecked:
		return ParseOutput(output.String(), cmd.Dir, files), nil
	case errors.Is(replaced.Err(), context.Canceled) && ctx.Err() == nil:
		return nil, ErrCanceled
	case ctx.Err() != nil:
		return nil, fmt.Errorf("p compile timed out after %s", r.Timeout)
	}
	errs := ParseOutput(output.String(), cmd.Dir, files)
	if waitErr != nil && len(errs) == 0 {
		// Failed without reporting an error we understand, such as a
		// command-line problem.
		return nil, fmt.Errorf("p compile failed: %v\n%s", waitErr, lastLines(output.String(), 20))
	}
	return errs, nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return strings.Join(lines[max(len(lines)-n, 0):], "\n")
}

func entries(dir string) map[string]bool {
	names := map[string]bool{}
	list, _ := os.ReadDir(dir)
	for _, e := range list {
		names[e.Name()] = true
	}
	return names
}

// removeNewEmpty removes the empty directories in dir that weren't there
// before. The build goes to a temporary directory, but the compiler still
// makes the project's output directory, PGenerated by default.
func removeNewEmpty(dir string, before map[string]bool) {
	list, _ := os.ReadDir(dir)
	for _, e := range list {
		if e.IsDir() && !before[e.Name()] {
			os.Remove(filepath.Join(dir, e.Name())) // fails unless empty
		}
	}
}
