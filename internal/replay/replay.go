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

	pproject "github.com/bencornia/ptools/internal/project"
	"github.com/bencornia/ptools/internal/ptool"
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

// ErrNoTool is returned when the command that runs the P tool can't be
// found. Callers say how to set it.
var ErrNoTool = errors.New("can't find the P tool")

// Bug is a bug PChecker found in a test case: the case it was in, which
// for a parameterised test is one of its cases, and the bug's trace.
type Bug struct {
	Test  string `json:"test"`
	Trace string `json:"trace"`
}

// Check compiles the P project in project and checks the test case named
// name, exploring up to schedules schedules, until PChecker finds a bug. A
// parameterised test can be named for all its cases, which P names
// <name>___<param>_<value>__<param>_<value>...: they're checked in turn,
// until one finds a bug. It returns the bug, whose trace it saves, with its
// schedule, as PCheckerOutput/prdb/<case>.trace.json and .schedule in the
// project, for replaying later; or nil if PChecker found none. progress, if
// not nil, gets a line as each stage starts, and output the P tool's own
// output as it runs.
func Check(name, project string, schedules int, compiler []string, progress, output io.Writer) (*Bug, error) {
	c, err := newChecker(project, compiler, progress)
	if err != nil {
		return nil, err
	}
	defer c.clean()
	c.output = output
	bug, _, _, err := c.check(name, schedules)
	return bug, err
}

// TestCase checks the test case named name as Check does, and returns the
// bug's trace. If no schedule finds a bug, it replays one schedule of the
// test case, or of the first of a parameterised test's, with --verbose,
// and returns that trace, which clean removes, and bug false.
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
	found, cases, dll, err := c.check(name, schedules)
	if err != nil {
		return "", false, nil, err
	}
	if found != nil {
		return found.Trace, true, c.clean, nil
	}

	one := cases[0].test
	c.say("PChecker found no bug in %s in %d schedules; replaying one run of %s", name, schedules, one)
	out, runErr := c.run("check", dll, "--testcase", one, "--schedules", "1", "--verbose", "--outdir", filepath.Join(c.dir, "run"))
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

// check compiles the project and checks the test case name. It returns the
// bug PChecker found, if any, the test cases it ran, which are name or its
// cases, and the assembly it checked.
func (c *checker) check(name string, schedules int) (*Bug, []testRun, string, error) {
	dll, err := c.compile()
	if err != nil {
		return nil, nil, "", err
	}

	c.say("Checking %s, for up to %d schedules", name, schedules)
	out, runErr := c.run("check", dll, "--testcase", name, "--schedules", strconv.Itoa(schedules), "--outdir", filepath.Join(c.dir, "check"))
	cases := ofTest(c.testRuns(out), name)
	if len(cases) == 0 {
		tests, _ := c.run("check", dll, "--list-tests")
		return nil, nil, "", fmt.Errorf("the project has no test case %s; its tests are: %s", name, strings.Join(listedTests(tests), ", "))
	}
	for _, r := range cases {
		if r.trace != "" {
			tracePath, err := c.save(r)
			if err != nil {
				return nil, nil, "", err
			}
			return &Bug{Test: r.test, Trace: tracePath}, cases, dll, nil
		}
	}
	if runErr != nil && !strings.Contains(string(out), "Found 0 bugs") {
		return nil, nil, "", fmt.Errorf("checking %s: %w", name, runErr)
	}
	return nil, cases, dll, nil
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
	// name is the project's, which names the assembly the compiler builds.
	name      string
	command   string
	args, env []string
	dir       string
	progress  io.Writer
	// output, if not nil, gets the P tool's output as it runs.
	output io.Writer
}

func newChecker(project string, compiler []string, progress io.Writer) (*checker, error) {
	if len(compiler) == 0 {
		compiler = []string{"p"}
	}
	command, env, err := ptool.Find(compiler)
	if err != nil {
		return nil, fmt.Errorf("%w: %q isn't on the PATH or in ~/.dotnet/tools", ErrNoTool, compiler[0])
	}
	pprojs, _ := filepath.Glob(filepath.Join(project, "*.pproj"))
	if len(pprojs) == 0 {
		return nil, fmt.Errorf("no .pproj file in %s to compile", project)
	}
	p, err := pproject.Parse(pprojs[0])
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "prdb-replay-")
	if err != nil {
		return nil, err
	}
	return &checker{project: project, pproj: p.Path, name: p.Name, command: command[0], args: command[1:], env: env, dir: dir, progress: progress}, nil
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
	var out bytes.Buffer
	var w io.Writer = &out
	if c.output != nil {
		w = io.MultiWriter(&out, c.output)
	}
	// The same writer for both, so they're written one at a time.
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("p %s: %w\n%s", args[0], err, strings.TrimSpace(out.String()))
	}
	return out.Bytes(), nil
}

// compile builds the project for PChecker, and returns the assembly to
// check. The build goes to the temporary directory, though the compiler
// still makes the project's output directory; compile removes it again
// if it made it and it's empty.
func (c *checker) compile() (string, error) {
	before := ptool.Names(c.project)
	c.say("Compiling %s", filepath.Base(c.pproj))
	build := filepath.Join(c.dir, "build")
	_, err := c.run("compile", "--pproj", c.pproj, "--outdir", build)
	ptool.RemoveNewEmpty(c.project, before)
	if err != nil {
		return "", err
	}
	return checkerDLL(build, c.name)
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
