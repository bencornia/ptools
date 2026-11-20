package compile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeCompiler writes a shell script standing in for `p` and returns a
// project file for it to "compile".
func fakeCompiler(t *testing.T, script string) (*Runner, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-p")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(dir, "App")
	if err := os.MkdirAll(filepath.Join(projectDir, "PSrc"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Runner{Command: []string{path}, Timeout: 10 * time.Second}, filepath.Join(projectDir, "App.pproj")
}

func TestCheckReportsErrors(t *testing.T) {
	r, pproj := fakeCompiler(t, `
echo "Type checking ..."
echo "[Error:] [PSrc/Client.p:3:7] could not find event 'eFoo'"
exit 1
`)
	client := filepath.Join(filepath.Dir(pproj), "PSrc", "Client.p")
	errs, err := r.Check(pproj, []string{client})
	if err != nil {
		t.Fatal(err)
	}
	want := Error{File: client, Line: 2, Column: 6, Message: "could not find event 'eFoo'"}
	if len(errs) != 1 || errs[0] != want {
		t.Errorf("got %+v, want [%+v]", errs, want)
	}
}

func TestCheckStopsOnceTypeCheckingPasses(t *testing.T) {
	r, pproj := fakeCompiler(t, `
echo "Type checking ..."
echo "Code generation for PChecker..."
sleep 30
echo "[Error:] [PSrc/Client.p:1:1] from the C# build, which isn't a type error"
`)
	start := time.Now()
	errs, err := r.Check(pproj, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 0 {
		t.Errorf("got %+v, want no errors", errs)
	}
	// Without stopping the compiler's children too, this would wait out
	// WaitDelay (2s) for the sleep holding the output open.
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s; should stop at code generation", elapsed)
	}
}

func TestNewerCheckCancelsOlder(t *testing.T) {
	// The first run sleeps; later ones finish at once.
	marker := filepath.Join(t.TempDir(), "ran")
	r, pproj := fakeCompiler(t, `
if [ -e `+marker+` ]; then exit 0; fi
touch `+marker+`
sleep 30
`)
	older := make(chan error, 1)
	go func() {
		_, err := r.Check(pproj, nil)
		older <- err
	}()
	// Let the first check start before replacing it.
	for start := time.Now(); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("first check never started")
		}
	}

	if _, err := r.Check(pproj, nil); err != nil {
		t.Fatalf("newer check: %v", err)
	}
	select {
	case err := <-older:
		if !errors.Is(err, ErrCanceled) {
			t.Errorf("older check returned %v, want ErrCanceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("older check wasn't canceled")
	}
}

func TestCheckTimesOut(t *testing.T) {
	r, pproj := fakeCompiler(t, `sleep 30`)
	r.Timeout = 200 * time.Millisecond
	if _, err := r.Check(pproj, nil); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("got %v, want a timeout", err)
	}
}

func TestCheckReportsUnparsedFailures(t *testing.T) {
	r, pproj := fakeCompiler(t, `
echo "Unknown option --pproj"
exit 2
`)
	if _, err := r.Check(pproj, nil); err == nil || !strings.Contains(err.Error(), "Unknown option --pproj") {
		t.Errorf("got %v, want the compiler's output in the error", err)
	}
}

func TestCheckArguments(t *testing.T) {
	record := filepath.Join(t.TempDir(), "record")
	r, pproj := fakeCompiler(t, `
pwd > `+record+`
echo "$@" >> `+record+`
`)
	if _, err := r.Check(pproj, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("unexpected record %q", data)
	}
	if lines[0] != filepath.Dir(pproj) {
		t.Errorf("ran in %s, want the project directory %s", lines[0], filepath.Dir(pproj))
	}
	args := strings.Fields(lines[1])
	if len(args) != 5 || args[0] != "compile" || args[1] != "--pproj" || args[2] != pproj || args[3] != "--outdir" {
		t.Fatalf("args = %v, want compile --pproj <pproj> --outdir <dir>", args)
	}
	if _, err := os.Stat(args[4]); !os.IsNotExist(err) {
		t.Errorf("output directory %s was left behind", args[4])
	}
	if filepath.Dir(args[4]) == filepath.Dir(pproj) {
		t.Errorf("output directory %s is inside the project", args[4])
	}
}

func TestAvailable(t *testing.T) {
	if err := (&Runner{Command: []string{"plsp-no-such-compiler"}}).Available(); err == nil {
		t.Error("missing compiler reported as available")
	}
	if err := (&Runner{Command: []string{"sh"}}).Available(); err != nil {
		t.Errorf("sh reported as unavailable: %v", err)
	}
}

// An editor started from a desktop may not have the PATH and DOTNET_ROOT a
// shell profile sets; p is found where `dotnet tool install --global` puts
// it, and run with that .NET.
func TestAvailableInDotnetTools(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("DOTNET_ROOT", "")
	r := &Runner{Command: []string{"p"}, Timeout: 10 * time.Second}
	if err := r.Available(); err == nil {
		t.Fatal("p reported as available with no p anywhere")
	}

	dotnet := filepath.Join(home, ".dotnet")
	os.MkdirAll(filepath.Join(dotnet, "tools"), 0o755)
	os.WriteFile(filepath.Join(dotnet, "dotnet"), nil, 0o755)
	seen := filepath.Join(home, "seen")
	script := "#!/bin/sh\necho \"$DOTNET_ROOT $PATH\" > " + seen + "\n"
	if err := os.WriteFile(filepath.Join(dotnet, "tools", "p"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := r.Available(); err != nil {
		t.Fatalf("p in ~/.dotnet/tools reported as unavailable: %v", err)
	}
	pproj := filepath.Join(home, "App", "App.pproj")
	os.MkdirAll(filepath.Dir(pproj), 0o755)
	if _, err := r.Check(pproj, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(seen)
	if want := dotnet + " " + dotnet + ":/usr/bin:/bin\n"; string(got) != want {
		t.Errorf("p ran with DOTNET_ROOT and PATH %q, not %q", got, want)
	}
}

// The compiler makes the project's output directory even when the build
// goes elsewhere; an empty one it made is removed, and nothing else is.
func TestCheckRemovesEmptyOutputDirectory(t *testing.T) {
	r, pproj := fakeCompiler(t, `
mkdir -p PGenerated Made
touch Made/file
echo "Type checking ..."
`)
	dir := filepath.Dir(pproj)
	os.Mkdir(filepath.Join(dir, "Empty"), 0o755) // the user's
	if _, err := r.Check(pproj, nil); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"PGenerated": false, "Made": true, "Empty": true, "PSrc": true} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != want {
			t.Errorf("%s exists: %v, want %v", name, err == nil, want)
		}
	}
	// A directory there before plsp first compiled is kept, even if it's
	// empty after a later check.
	os.Mkdir(filepath.Join(dir, "PGenerated"), 0o755)
	r2, _ := fakeCompiler(t, "echo")
	r2.Command = r.Command
	r2.Check(pproj, nil)
	if _, err := os.Stat(filepath.Join(dir, "PGenerated")); err != nil {
		t.Error("removed an output directory that was there before")
	}
}
