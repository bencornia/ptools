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
	"regexp"
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
// bug. A parameterised test can be named for all its cases, which P names
// <name>___<param>_<value>__<param>_<value>...: they're checked in turn,
// until one finds a bug. It returns the bug's trace, which it also saves,
// with its schedule, as PCheckerOutput/prdb/<case>.trace.json and
// .schedule in the project, for replaying later.
//
// If no schedule finds a bug, it replays one schedule of the test case,
// or of the first of a parameterised test's, with --verbose, and returns
// that trace, which clean removes, and bug false.
func TestCase(name, project string, schedules int, compiler []string, progress io.Writer) (tracePath string, bug bool, clean func(), err error) {
	c, err := newChecker(project, compiler, progress)
	if err != nil {
		return "", false, nil, err
	}
	defer func() {
		if err != nil {
			c.clean()
		}
	}()
	dll, err := c.compile()
	if err != nil {
		return "", false, nil, err
	}

	c.say("Checking %s, for up to %d schedules", name, schedules)
	out, runErr := c.run("check", dll, "--testcase", name, "--schedules", strconv.Itoa(schedules), "--outdir", filepath.Join(c.dir, "check"))
	cases := ofTest(c.testRuns(out), name)
	if len(cases) == 0 {
		tests, _ := c.run("check", dll, "--list-tests")
		return "", false, nil, fmt.Errorf("the project has no test case %s; its tests are: %s", name, strings.Join(listedTests(tests), ", "))
	}
	for _, r := range cases {
		if r.trace != "" {
			tracePath, err := c.save(r)
			return tracePath, true, c.clean, err
		}
	}
	if runErr != nil && !strings.Contains(string(out), "Found 0 bugs") {
		return "", false, nil, fmt.Errorf("checking %s: %w", name, runErr)
	}

	one := cases[0].test
	c.say("PChecker found no bug in %s in %d schedules; replaying one run of %s", name, schedules, one)
	out, runErr = c.run("check", dll, "--testcase", one, "--schedules", "1", "--verbose", "--outdir", filepath.Join(c.dir, "run"))
	for _, r := range c.testRuns(out) {
		if r.test == one && r.trace != "" {
			return r.trace, false, c.clean, nil
		}
	}
	if runErr == nil {
		runErr = errors.New("p check wrote no trace")
	}
	return "", false, nil, fmt.Errorf("running %s: %w", one, runErr)
}

// save copies a bug's trace and schedule into the project, and returns
// where the trace went.
func (c *checker) save(r testRun) (string, error) {
	saved := filepath.Join(c.project, "PCheckerOutput", "prdb")
	if err := os.MkdirAll(saved, 0o755); err != nil {
		return "", err
	}
	tracePath := filepath.Join(saved, r.test+".trace.json")
	if err := copyFile(r.trace, tracePath); err != nil {
		return "", err
	}
	schedule := strings.TrimSuffix(r.trace, ".trace.json") + ".schedule"
	if err := copyFile(schedule, filepath.Join(saved, r.test+".schedule")); err != nil {
		return "", err
	}
	rel, _ := filepath.Rel(c.project, saved)
	c.say("Found a bug in %s; saved its trace and schedule in %s", r.test, filepath.Join(rel, r.test)+".*")
	return tracePath, nil
}

// testRun is a test case p check ran, and the trace it wrote, if any.
type testRun struct {
	test, trace string
}

// testRuns reads p check's output for the test cases it ran, each under a
// ".. Test case :: <name>" heading, and the trace each wrote: of a bug, or
// with --verbose, of every schedule.
func (c *checker) testRuns(out []byte) []testRun {
	var runs []testRun
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if test, ok := strings.CutPrefix(line, ".. Test case :: "); ok {
			runs = append(runs, testRun{test: strings.TrimSpace(test)})
			continue
		}
		if path, ok := strings.CutPrefix(line, "..... Writing "); ok && len(runs) > 0 && strings.HasSuffix(path, ".trace.json") {
			if last := &runs[len(runs)-1]; last.trace == "" {
				if !filepath.IsAbs(path) {
					path = filepath.Join(c.project, path)
				}
				last.trace = path
			}
		}
	}
	return runs
}

// ofTest picks the runs of the test case name: the one so named, or else
// a parameterised test's cases. -tc runs every test case whose name starts
// with the one given, so others may have run too.
func ofTest(runs []testRun, name string) []testRun {
	for _, r := range runs {
		if r.test == name {
			return []testRun{r}
		}
	}
	var cases []testRun
	for _, r := range runs {
		if strings.HasPrefix(r.test, name+"___") {
			cases = append(cases, r)
		}
	}
	return cases
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
	return checkerDLL(build, projectName(c.pproj))
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

// projectName returns the name the compiler gives a project's assembly:
// its <ProjectName>, or else the .pproj file's name.
func projectName(pproj string) string {
	data, _ := os.ReadFile(pproj)
	if m := projectNameElement.FindSubmatch(data); m != nil {
		return strings.TrimSpace(string(m[1]))
	}
	return strings.TrimSuffix(filepath.Base(pproj), ".pproj")
}

var projectNameElement = regexp.MustCompile(`<ProjectName>([^<]+)</ProjectName>`)

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
