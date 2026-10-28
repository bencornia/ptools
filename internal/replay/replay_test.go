package replay

import (
	"os"
	"os/exec"
	"path/filepath"
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

	tracePath, err := TestCase("tcPingPong", project, 100, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(project, "PCheckerOutput", "prdb", "tcPingPong.trace.json"); tracePath != want {
		t.Errorf("trace saved as %s, not %s", tracePath, want)
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

	_, err = TestCase("tcPing", project, 10, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "no test case tcPing; its tests are: tcPingPong") {
		t.Errorf("checking a test case that doesn't exist: %v", err)
	}

	// Fix the bug.
	src := filepath.Join(project, "PSrc", "PingPong.p")
	text, _ := os.ReadFile(src)
	os.WriteFile(src, []byte(strings.Replace(string(text), "req.n == 3", "req.n == 99", 1)), 0o644)
	_, err = TestCase("tcPingPong", project, 10, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "found no bug in tcPingPong in 10 schedules") {
		t.Errorf("checking a test case with no bug: %v", err)
	}
}

func TestTestOutput(t *testing.T) {
	// -tc tcPingPong runs tcPingPong2 as well.
	out := []byte(`.. Running test cases with prefix 'tcPingPong':
.. Test case :: tcPingPong
..... Writing /out/BugFinding/PingPong_0_0.txt
..... Writing /out/BugFinding/PingPong_0_0.trace.json
..... Writing /out/BugFinding/PingPong_0_0.schedule
.. Test case :: tcPingPong2
..... Writing /out/BugFinding/PingPong_0_1.trace.json
`)
	if ran, trace := testOutput(out, "tcPingPong2"); !ran || trace != "/out/BugFinding/PingPong_0_1.trace.json" {
		t.Errorf("tcPingPong2: ran %v, trace %q", ran, trace)
	}
	if ran, trace := testOutput(out, "tcPingPong"); !ran || trace != "/out/BugFinding/PingPong_0_0.trace.json" {
		t.Errorf("tcPingPong: ran %v, trace %q", ran, trace)
	}
	if ran, _ := testOutput(out, "tcPing"); ran {
		t.Error("ran tcPing")
	}
}
