package replay

import (
	"os"
	"os/exec"
	"path/filepath"
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
