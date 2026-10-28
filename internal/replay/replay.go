// Package replay turns a PChecker schedule into a trace, by compiling the
// project and replaying the schedule with p check.
package replay

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Schedule compiles the P project in project and replays schedule, a
// .schedule file PChecker wrote, with --verbose, so that it writes a
// trace. It returns the trace's path, in a temporary directory that clean
// removes. compiler runs the P tool, as in []string{"p"}; progress, if not
// nil, gets a line as each stage starts.
//
// The build goes to the temporary directory too, though the compiler
// still makes the project's output directory; Schedule removes it again
// if it made it and it's empty.
func Schedule(schedule, project string, compiler []string, progress io.Writer) (tracePath string, clean func(), err error) {
	if len(compiler) == 0 {
		compiler = []string{"p"}
	}
	command, env, err := findCompiler(compiler[0])
	if err != nil {
		return "", nil, err
	}
	schedule, err = filepath.Abs(schedule)
	if err != nil {
		return "", nil, err
	}
	pprojs, _ := filepath.Glob(filepath.Join(project, "*.pproj"))
	if len(pprojs) == 0 {
		return "", nil, fmt.Errorf("no .pproj file in %s to compile", project)
	}
	dir, err := os.MkdirTemp("", "prdb-replay-")
	if err != nil {
		return "", nil, err
	}
	clean = func() { os.RemoveAll(dir) }
	defer func() {
		if err != nil {
			clean()
		}
	}()

	say := func(format string, args ...any) {
		if progress != nil {
			fmt.Fprintf(progress, format+"\n", args...)
		}
	}
	run := func(args ...string) error {
		cmd := exec.Command(command, append(slices.Clone(compiler[1:]), args...)...)
		cmd.Dir = project
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s %s: %w\n%s", filepath.Base(compiler[0]), args[0], err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	before := entries(project)
	say("Compiling %s", filepath.Base(pprojs[0]))
	build := filepath.Join(dir, "build")
	err = run("compile", "--pproj", pprojs[0], "--outdir", build)
	removeNewEmpty(project, before)
	if err != nil {
		return "", nil, err
	}
	dll, err := checkerDLL(build, strings.TrimSuffix(filepath.Base(pprojs[0]), ".pproj"))
	if err != nil {
		return "", nil, err
	}

	say("Replaying %s", filepath.Base(schedule))
	check := filepath.Join(dir, "check")
	// p check exits non-zero when the replay finds the bug, which is the
	// usual case; whether it wrote a trace is what matters.
	runErr := run("check", dll, "--replay", schedule, "--verbose", "--outdir", check)
	traces, _ := filepath.Glob(filepath.Join(check, "*", "*.trace.json"))
	if len(traces) == 0 {
		if runErr == nil {
			runErr = errors.New("p check wrote no trace")
		}
		return "", nil, fmt.Errorf("replaying %s: %w", filepath.Base(schedule), runErr)
	}
	return traces[0], clean, nil
}

// findCompiler finds the P tool, name, and the environment to run it in.
// Editors started from a desktop often lack the PATH and DOTNET_ROOT a
// shell profile sets, so when name is the default "p" and isn't on the
// PATH, it looks where `dotnet tool install --global` puts it, and runs it
// with the .NET it was installed with.
func findCompiler(name string) (command string, env []string, err error) {
	env = os.Environ()
	if path, err := exec.LookPath(name); err == nil {
		return path, env, nil
	}
	home, _ := os.UserHomeDir()
	if name == "p" && home != "" {
		tool := filepath.Join(home, ".dotnet", "tools", "p")
		if _, err := os.Stat(tool); err == nil {
			root := os.Getenv("DOTNET_ROOT")
			if root == "" {
				root = filepath.Join(home, ".dotnet")
			}
			if _, err := os.Stat(filepath.Join(root, "dotnet")); err == nil {
				env = append(env, "DOTNET_ROOT="+root, "PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"))
			}
			return tool, env, nil
		}
	}
	return "", nil, fmt.Errorf("replaying a schedule needs the P compiler, and %q isn't on the PATH; set the launch configuration's compiler to the command that runs it", name)
}

// checkerDLL finds the assembly the compiler built for PChecker: the one
// named after the project, outside obj/.
func checkerDLL(build, name string) (string, error) {
	var found string
	filepath.WalkDir(build, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return filepath.SkipDir
		}
		if d.IsDir() && d.Name() == "obj" {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.EqualFold(d.Name(), name+".dll") {
			found = path
		}
		return nil
	})
	if found == "" {
		return "", fmt.Errorf("the compiler built no %s.dll", name)
	}
	return found, nil
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
// before.
func removeNewEmpty(dir string, before map[string]bool) {
	list, _ := os.ReadDir(dir)
	for _, e := range list {
		if e.IsDir() && !before[e.Name()] {
			os.Remove(filepath.Join(dir, e.Name())) // fails unless empty
		}
	}
}
