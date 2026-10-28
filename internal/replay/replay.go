// Package replay makes traces with PChecker: by replaying a schedule, or
// by checking a test case until it finds a bug. Either way it compiles the
// project first, so the trace matches the source as it is.
package replay

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Schedule compiles the P project in project and replays schedule, a
// .schedule file PChecker wrote, with --verbose, so that it writes a
// trace. It returns the trace's path, in a temporary directory that clean
// removes. compiler runs the P tool, as in []string{"p"}; progress, if not
// nil, gets a line as each stage starts.
func Schedule(schedule, project string, compiler []string, progress io.Writer) (tracePath string, clean func(), err error) {
	schedule, err = filepath.Abs(schedule)
	if err != nil {
		return "", nil, err
	}
	c, err := newChecker(project, compiler, progress)
	if err != nil {
		return "", nil, err
	}
	defer func() {
		if err != nil {
			c.clean()
		}
	}()
	dll, err := c.compile()
	if err != nil {
		return "", nil, err
	}

	c.say("Replaying %s", filepath.Base(schedule))
	check := filepath.Join(c.dir, "check")
	// p check exits non-zero when the replay finds the bug, which is the
	// usual case; whether it wrote a trace is what matters.
	_, runErr := c.run("check", dll, "--replay", schedule, "--verbose", "--outdir", check)
	traces, _ := filepath.Glob(filepath.Join(check, "*", "*.trace.json"))
	if len(traces) == 0 {
		if runErr == nil {
			runErr = errors.New("p check wrote no trace")
		}
		return "", nil, fmt.Errorf("replaying %s: %w", filepath.Base(schedule), runErr)
	}
	return traces[0], c.clean, nil
}

// TestCase compiles the P project in project and checks the test case
// named name, exploring up to schedules schedules, until PChecker finds a
// bug. It returns the bug's trace, which it also saves, with its schedule,
// as PCheckerOutput/prdb/<name>.trace.json and .schedule in the project,
// for replaying later. If no schedule finds a bug, it returns an error.
func TestCase(name, project string, schedules int, compiler []string, progress io.Writer) (tracePath string, err error) {
	c, err := newChecker(project, compiler, progress)
	if err != nil {
		return "", err
	}
	defer c.clean()
	dll, err := c.compile()
	if err != nil {
		return "", err
	}

	c.say("Checking %s, for up to %d schedules", name, schedules)
	out, runErr := c.run("check", dll, "--testcase", name, "--schedules", strconv.Itoa(schedules), "--outdir", filepath.Join(c.dir, "check"))
	ran, found := testOutput(out, name)
	switch {
	case !ran:
		tests, _ := c.run("check", dll, "--list-tests")
		return "", fmt.Errorf("the project has no test case %s; its tests are: %s", name, strings.Join(listedTests(tests), ", "))
	case found == "":
		if runErr != nil {
			return "", fmt.Errorf("checking %s: %w", name, runErr)
		}
		return "", fmt.Errorf("PChecker found no bug in %s in %d schedules", name, schedules)
	}

	saved := filepath.Join(project, "PCheckerOutput", "prdb")
	if err := os.MkdirAll(saved, 0o755); err != nil {
		return "", err
	}
	tracePath = filepath.Join(saved, name+".trace.json")
	if err := copyFile(found, tracePath); err != nil {
		return "", err
	}
	schedule := strings.TrimSuffix(found, ".trace.json") + ".schedule"
	if err := copyFile(schedule, filepath.Join(saved, name+".schedule")); err != nil {
		return "", err
	}
	rel, _ := filepath.Rel(project, saved)
	c.say("Found a bug; saved its trace and schedule in %s", filepath.Join(rel, name)+".*")
	return tracePath, nil
}

// testOutput reads p check's output for whether it ran the test case
// name, and the trace it wrote for it if it found a bug. -tc runs every
// test case whose name starts with the one given, each under a
// ".. Test case :: <name>" heading.
func testOutput(out []byte, name string) (ran bool, trace string) {
	current := ""
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if test, ok := strings.CutPrefix(line, ".. Test case :: "); ok {
			current = strings.TrimSpace(test)
			ran = ran || current == name
			continue
		}
		if path, ok := strings.CutPrefix(line, "..... Writing "); ok && current == name && trace == "" && strings.HasSuffix(path, ".trace.json") {
			trace = path
		}
	}
	return ran, trace
}

// listedTests reads the test cases from p check --list-tests.
func listedTests(out []byte) []string {
	var tests []string
	listing := false
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, ".. List of test cases"):
			listing = true
		case listing && (line == "" || strings.HasPrefix(line, ".")):
			listing = false
		case listing:
			tests = append(tests, line)
		}
	}
	return tests
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o644)
}

// checker runs the P tool on a project, with its output in a temporary
// directory.
type checker struct {
	project, pproj string
	command        string
	args, env      []string
	dir            string
	progress       io.Writer
}

func newChecker(project string, compiler []string, progress io.Writer) (*checker, error) {
	if len(compiler) == 0 {
		compiler = []string{"p"}
	}
	command, env, err := findCompiler(compiler[0])
	if err != nil {
		return nil, err
	}
	pprojs, _ := filepath.Glob(filepath.Join(project, "*.pproj"))
	if len(pprojs) == 0 {
		return nil, fmt.Errorf("no .pproj file in %s to compile", project)
	}
	dir, err := os.MkdirTemp("", "prdb-replay-")
	if err != nil {
		return nil, err
	}
	return &checker{project: project, pproj: pprojs[0], command: command, args: compiler[1:], env: env, dir: dir, progress: progress}, nil
}

func (c *checker) clean() { os.RemoveAll(c.dir) }

func (c *checker) say(format string, args ...any) {
	if c.progress != nil {
		fmt.Fprintf(c.progress, format+"\n", args...)
	}
}

// run runs the P tool with args in the project, and returns its output.
func (c *checker) run(args ...string) ([]byte, error) {
	cmd := exec.Command(c.command, append(slices.Clone(c.args), args...)...)
	cmd.Dir = c.project
	cmd.Env = c.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("p %s: %w\n%s", args[0], err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// compile builds the project for PChecker, and returns the assembly to
// check. The build goes to the temporary directory, though the compiler
// still makes the project's output directory; compile removes it again
// if it made it and it's empty.
func (c *checker) compile() (string, error) {
	before := entries(c.project)
	c.say("Compiling %s", filepath.Base(c.pproj))
	build := filepath.Join(c.dir, "build")
	_, err := c.run("compile", "--pproj", c.pproj, "--outdir", build)
	removeNewEmpty(c.project, before)
	if err != nil {
		return "", err
	}
	return checkerDLL(build, strings.TrimSuffix(filepath.Base(c.pproj), ".pproj"))
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
