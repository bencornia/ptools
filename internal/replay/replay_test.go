package replay

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bencornia/prdb/internal/trace"
)

func TestSchedule(t *testing.T) {
	if _, err := exec.LookPath("p"); err != nil {
		t.Skip("the P compiler isn't installed")
	}
	// Work on a copy, to see what the compiler leaves in the project.
	project := t.TempDir()
	if err := os.CopyFS(project, os.DirFS("../../testdata/pingpong")); err != nil {
		t.Fatal(err)
	}
	tracePath, clean, err := Schedule(filepath.Join(project, "bug.schedule"), project, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clean()

	replayed, err := trace.Load(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	found, err := trace.Load(filepath.Join(project, "bug.trace.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed) != len(found) {
		t.Fatalf("replayed %d steps, PChecker found the bug in %d", len(replayed), len(found))
	}
	for i := range found {
		if replayed[i].Log != found[i].Log {
			t.Errorf("step %d: replayed %q, found %q", i, replayed[i].Log, found[i].Log)
		}
	}

	if _, err := os.Stat(filepath.Join(project, "PGenerated")); !os.IsNotExist(err) {
		t.Errorf("the compiler's empty output directory is left in the project")
	}
	clean()
	if _, err := os.Stat(tracePath); !os.IsNotExist(err) {
		t.Errorf("clean left the trace")
	}
}

func TestScheduleWithoutProject(t *testing.T) {
	if _, err := exec.LookPath("p"); err != nil {
		t.Skip("the P compiler isn't installed")
	}
	if _, _, err := Schedule("x.schedule", t.TempDir(), nil, nil); err == nil {
		t.Error("replayed a schedule with no project")
	}
}

// An editor started from a desktop may not have the PATH and DOTNET_ROOT
// a shell profile sets.
func TestScheduleWithoutShellEnvironment(t *testing.T) {
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".dotnet", "tools", "p")); err != nil {
		t.Skip("the P compiler isn't installed as a global .NET tool")
	}
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("DOTNET_ROOT", "")
	project := t.TempDir()
	if err := os.CopyFS(project, os.DirFS("../../testdata/pingpong")); err != nil {
		t.Fatal(err)
	}
	tracePath, clean, err := Schedule(filepath.Join(project, "bug.schedule"), project, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	clean()
	if tracePath == "" {
		t.Error("no trace")
	}
}

func TestTestCase(t *testing.T) {
	if _, err := exec.LookPath("p"); err != nil {
		t.Skip("the P compiler isn't installed")
	}
	project := t.TempDir()
	if err := os.CopyFS(project, os.DirFS("../../testdata/pingpong")); err != nil {
		t.Fatal(err)
	}

	tracePath, bug, clean, err := TestCase("tcPingPong", project, 100, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	clean()
	if want := filepath.Join(project, "PCheckerOutput", "prdb", "tcPingPong.trace.json"); tracePath != want || !bug {
		t.Errorf("trace saved as %s, a bug's: %v; want %s", tracePath, bug, want)
	}
	steps, err := trace.Load(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	if last := steps[len(steps)-1]; last.Kind != "AssertionFailure" {
		t.Errorf("the trace ends with %s", last.Kind)
	}
	if _, err := os.Stat(filepath.Join(project, "PCheckerOutput", "prdb", "tcPingPong.schedule")); err != nil {
		t.Errorf("the schedule isn't saved: %v", err)
	}

	_, _, _, err = TestCase("tcPing", project, 10, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "no test case tcPing; its tests are: tcPingPong") {
		t.Errorf("checking a test case that doesn't exist: %v", err)
	}

	// Fix the bug: one run is replayed instead.
	src := filepath.Join(project, "PSrc", "PingPong.p")
	text, _ := os.ReadFile(src)
	os.WriteFile(src, []byte(strings.Replace(string(text), "req.n == 3", "req.n == 99", 1)), 0o644)
	var progress strings.Builder
	tracePath, bug, clean, err = TestCase("tcPingPong", project, 10, nil, &progress)
	if err != nil {
		t.Fatal(err)
	}
	defer clean()
	if bug {
		t.Error("a run with no bug is reported as a bug's")
	}
	if !strings.Contains(progress.String(), "PChecker found no bug in tcPingPong in 10 schedules; replaying one run of tcPingPong\n") {
		t.Errorf("progress %q", progress.String())
	}
	steps, err = trace.Load(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	if last := steps[len(steps)-1]; last.Kind == "AssertionFailure" || last.Log != "got pong 5" {
		t.Errorf("the run ends with %s %q", last.Kind, last.Log)
	}
}

func TestParameterisedTestCase(t *testing.T) {
	if _, err := exec.LookPath("p"); err != nil {
		t.Skip("the P compiler isn't installed")
	}
	project := t.TempDir()
	if err := os.CopyFS(project, os.DirFS("../../testdata/params")); err != nil {
		t.Fatal(err)
	}

	// Named for all its cases, the one that fails is found.
	var progress strings.Builder
	tracePath, bug, clean, err := TestCase("tcHello", project, 10, nil, &progress)
	if err != nil {
		t.Fatal(err)
	}
	clean()
	if want := filepath.Join(project, "PCheckerOutput", "prdb", "tcHello___given_2.trace.json"); tracePath != want || !bug {
		t.Errorf("trace saved as %s, a bug's: %v; want %s", tracePath, bug, want)
	}
	if !strings.Contains(progress.String(), "Found a bug in tcHello___given_2; saved its trace and schedule in PCheckerOutput/prdb/tcHello___given_2.*\n") {
		t.Errorf("progress %q", progress.String())
	}

	// A case that passes is run once.
	for name, printed := range map[string]string{"tcHello___given_1": "hello 1", "tcQuiet": "quiet"} {
		tracePath, bug, clean, err := TestCase(name, project, 10, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		steps, err := trace.Load(tracePath)
		clean()
		if err != nil {
			t.Fatal(err)
		}
		if bug || steps[len(steps)-1].Log != printed {
			t.Errorf("%s: a bug's %v, ending %q", name, bug, steps[len(steps)-1].Log)
		}
	}

	_, _, _, err = TestCase("tcHello___given_3", project, 10, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "its tests are: tcHello___given_0, tcHello___given_1, tcHello___given_2, tcQuiet") {
		t.Errorf("checking a case that doesn't exist: %v", err)
	}
}

func TestTestRuns(t *testing.T) {
	// -tc tcPingPong runs tcPingPong2 as well, and -tc tcHello all its
	// cases.
	out := []byte(`.. Running test cases with prefix 'tcPingPong':
.. Test case :: tcPingPong
..... Writing /out/BugFinding/PingPong_0_0.txt
..... Writing /out/BugFinding/PingPong_0_0.trace.json
..... Writing /out/BugFinding/PingPong_0_0.schedule
.. Test case :: tcPingPong2
..... Writing BugFinding/PingPong_0_1.trace.json
.. Test case :: tcHello___given_0
..... Found 0 bugs.
.. Test case :: tcHello___given_1
..... Writing /out/BugFinding/PingPong_0_3.trace.json
`)
	c := &checker{project: "/project"}
	runs := c.testRuns(out)
	for name, want := range map[string][]testRun{
		"tcPingPong":  {{"tcPingPong", "/out/BugFinding/PingPong_0_0.trace.json"}},
		"tcPingPong2": {{"tcPingPong2", "/project/BugFinding/PingPong_0_1.trace.json"}}, // relative to the project
		"tcHello":     {{"tcHello___given_0", ""}, {"tcHello___given_1", "/out/BugFinding/PingPong_0_3.trace.json"}},
		"tcPing":      nil,
	} {
		if got := ofTest(runs, name); !slices.Equal(got, want) {
			t.Errorf("%s: %+v, want %+v", name, got, want)
		}
	}
}
