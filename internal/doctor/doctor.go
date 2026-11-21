// Package doctor checks that ptools is set up to work: the four commands,
// the P compiler, and the editors that run them, Helix, Neovim and VS Code.
// It can fix most of what it finds: it installs the P compiler, with .NET
// if need be, configures Helix and builds the grammar for it, adds a
// plugin to Neovim's configuration that starts plsp, and installs the VS
// Code extension.
package doctor

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bencornia/ptools/internal/version"
)

// Tools are the ptools commands.
var Tools = []string{"plsp", "prdb", "pfmt", "pdoc"}

// Doctor checks the setup and reports what it finds.
type Doctor struct {
	// Out is where the report goes.
	Out io.Writer
	// Fix says to fix the problems found, where it can.
	Fix bool

	// Version is the ptools release the tools and extension should be.
	Version string
	// GrammarRepo and GrammarRev are the tree-sitter-p repository and
	// commit editors should build the grammar from.
	GrammarRepo, GrammarRev string
	// DotnetInstall is the URL of Microsoft's dotnet-install.sh.
	DotnetInstall string
	// Releases is the URL ptools' release files are under, by tag.
	Releases string

	problems, fixable int
}

// New returns a Doctor for this ptools release, reporting to out.
func New(out io.Writer, fix bool) *Doctor {
	return &Doctor{
		Out:           out,
		Fix:           fix,
		Version:       version.Version,
		GrammarRepo:   "https://github.com/bencornia/tree-sitter-p",
		GrammarRev:    version.Grammar,
		DotnetInstall: "https://dot.net/v1/dotnet-install.sh",
		Releases:      "https://github.com/bencornia/ptools/releases/download",
	}
}

// Run checks everything, fixing what it can if d.Fix is set, and reports
// whether everything is set up.
func (d *Doctor) Run() bool {
	d.tools()
	d.pCompiler()
	d.helix()
	d.neovim()
	d.vscode()
	fmt.Fprintln(d.Out)
	switch {
	case d.problems == 0:
		fmt.Fprintln(d.Out, "Everything is set up.")
	case d.fixable == 0:
		fmt.Fprintf(d.Out, "%s to fix by hand.\n", count(d.problems, "problem"))
	case d.fixable == d.problems:
		fmt.Fprintf(d.Out, "%s. plsp doctor --fix fixes them.\n", count(d.problems, "problem"))
	default:
		fmt.Fprintf(d.Out, "%s. plsp doctor --fix fixes %d of them.\n", count(d.problems, "problem"), d.fixable)
	}
	return d.problems == 0
}

func (d *Doctor) section(name string) { fmt.Fprintf(d.Out, "\n%s\n", name) }

func (d *Doctor) skip(name, why string) { fmt.Fprintf(d.Out, "\n%s: %s\n", name, why) }

func (d *Doctor) good(format string, args ...any) { d.line("  ✓ ", format, args...) }

func (d *Doctor) note(format string, args ...any) { d.line("  ! ", format, args...) }

// code shows a command or line to use, under the line before.
func (d *Doctor) code(text string) { d.line("      ", "%s", text) }

// bad reports a problem. If fixable is set and d.Fix is too, it returns true
// for the caller to fix it now, reporting the fix with doing and anything
// that goes wrong with failed. Otherwise it counts the problem.
func (d *Doctor) bad(fixable bool, format string, args ...any) bool {
	d.line("  ✗ ", format, args...)
	if fixable && d.Fix {
		return true
	}
	d.problems++
	if fixable {
		d.fixable++
	}
	return false
}

func (d *Doctor) doing(format string, args ...any) { d.line("    → ", format, args...) }

func (d *Doctor) failed(err error) {
	d.line("    ✗ ", "%v", err)
	d.problems++
}

func (d *Doctor) line(mark, format string, args ...any) {
	fmt.Fprintf(d.Out, "%s%s\n", mark, fmt.Sprintf(format, args...))
}

// tools checks that the commands are on the PATH, and are all this release.
func (d *Doctor) tools() {
	d.section("ptools")
	self := ""
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			self = filepath.Dir(exe)
		}
	}
	var dirs []string
	offPath, stale := false, false
	for _, name := range Tools {
		path, err := exec.LookPath(name)
		if err != nil {
			if self != "" && exists(filepath.Join(self, name)) {
				offPath = true
			} else {
				d.bad(false, "%s isn't installed", name)
				stale = true
			}
			continue
		}
		out, err := run(nil, path, "--version")
		if v := strings.TrimSpace(out); err != nil || v != d.Version {
			if err != nil || strings.ContainsAny(v, " \n") {
				v = "an older one"
			}
			d.bad(false, "%s, at %s, is version %s, not %s", name, tilde(path), v, d.Version)
			stale = true
			continue
		}
		if dir := tilde(filepath.Dir(path)); !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	if offPath {
		d.bad(false, "%s isn't on your PATH, and editors look for the tools there. Add it in your shell's profile:", tilde(self))
		d.code(`export PATH="` + shellPath(self) + `:$PATH"`)
	}
	if stale {
		d.note("To install them again, run:")
		d.code("curl -fsSL https://raw.githubusercontent.com/bencornia/ptools/main/install.sh | sh")
	}
	if !offPath && !stale {
		d.good("plsp, prdb, pfmt and pdoc %s, in %s", d.Version, strings.Join(dirs, ", "))
	}
}

// run runs a command with env, or this process's environment if env is nil,
// and returns what it printed.
func run(env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// runIn is run in a directory, failing with the command's output.
func runIn(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %v%s", name, strings.Join(args, " "), err, indent(out))
	}
	return nil
}

// download saves url in dir, under the name it ends with, and returns the
// file's path.
func download(url, dir string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	path := filepath.Join(dir, filepath.Base(url))
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return "", fmt.Errorf("downloading %s: %v", url, err)
	}
	return path, f.Close()
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// tilde writes path with ~ for the home directory.
func tilde(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return filepath.Join("~", rest)
	}
	return path
}

// shellPath writes path for a shell's double quotes, with $HOME for the
// home directory.
func shellPath(path string) string {
	if t := tilde(path); strings.HasPrefix(t, "~") {
		return "$HOME" + t[1:]
	}
	return path
}

// firstLine returns the first non-empty line of out.
func firstLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return "no output"
}

// indent indents a command's output to follow an error.
func indent(out []byte) string {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 10 {
		lines = append([]string{"..."}, lines[len(lines)-10:]...)
	}
	return "\n        " + strings.Join(lines, "\n        ")
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
