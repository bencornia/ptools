package doctor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/bencornia/ptools/internal/ptool"
)

// pCompiler checks that plsp and prdb can run the P tool, `p`, and installs
// it, with .NET 8 if need be, to fix that.
func (d *Doctor) pCompiler() {
	d.section("P compiler")
	problem := d.checkP()
	if problem == "" {
		return
	}
	if !d.bad(runtime.GOOS != "windows", "%s", problem) {
		if runtime.GOOS == "windows" {
			d.note("Install the .NET 8 SDK, from https://dotnet.microsoft.com/download/dotnet/8.0, and then P:")
			d.code("dotnet tool install --global P")
		}
		return
	}
	if err := d.installP(); err != nil {
		d.failed(err)
		return
	}
	if problem := d.checkP(); problem != "" {
		d.failed(errors.New(problem))
	}
}

var pVersion = regexp.MustCompile(`P version (\S+)`)

// checkP reports the P tool plsp and prdb would run, or returns what's
// wrong with it.
func (d *Doctor) checkP() string {
	command, env, err := ptool.Find([]string{"p"})
	if err != nil {
		return "P isn't installed"
	}
	out, err := run(env, command[0], "--version")
	if err != nil {
		return "P, at " + tilde(command[0]) + ", doesn't run: " + firstLine(out)
	}
	path := command[0]
	if abs, err := exec.LookPath(path); err == nil {
		path, _ = filepath.Abs(abs)
	}
	v := "of an unknown version"
	if m := pVersion.FindStringSubmatch(out); m != nil {
		v = strings.TrimSuffix(m[1], ".0")
	}
	d.good("P %s, at %s", v, tilde(path))
	if _, err := exec.LookPath("p"); err != nil {
		// ptool.Find found it in ~/.dotnet/tools.
		d.note("plsp and prdb find it there, but your shell doesn't. To run p yourself, add this to your shell's profile:")
		add := `export PATH="$PATH:$HOME/.dotnet/tools"`
		if env != nil {
			root := shellPath(dotnetRoot())
			add = `export DOTNET_ROOT="` + root + `" PATH="$PATH:` + root + `:$HOME/.dotnet/tools"`
		}
		d.code(add)
	}
	return ""
}

// dotnetRoot is where ptool.Find runs .NET from, and so where to install it.
func dotnetRoot() string {
	if root := os.Getenv("DOTNET_ROOT"); root != "" {
		return root
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dotnet")
}

// installP installs P as a .NET tool, installing the .NET 8 SDK first if
// there isn't one, as P 3 needs.
func (d *Doctor) installP() error {
	root := dotnetRoot()
	dotnet := findDotnet8(root)
	if dotnet == "" {
		d.doing("installing .NET 8 into %s, with Microsoft's dotnet-install.sh", tilde(root))
		tmp, err := os.MkdirTemp("", "plsp-doctor")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		script, err := download(d.DotnetInstall, tmp)
		if err != nil {
			return err
		}
		if err := runIn(tmp, nil, "bash", script, "--channel", "8.0", "--install-dir", root, "--no-path"); err != nil {
			return err
		}
		dotnet = filepath.Join(root, "dotnet")
	}
	env := append(os.Environ(), "DOTNET_CLI_TELEMETRY_OPTOUT=1", "DOTNET_NOLOGO=1")
	if filepath.Dir(dotnet) == root {
		env = append(env, "DOTNET_ROOT="+root)
	}
	verb := "install"
	home, _ := os.UserHomeDir()
	if exists(filepath.Join(home, ".dotnet", "tools", "p")) {
		verb = "update"
	}
	d.doing("installing P, with dotnet tool %s --global P", verb)
	return runIn("", env, dotnet, "tool", verb, "--global", "P")
}

// findDotnet8 returns a dotnet command with a .NET 8 SDK, from root or the
// PATH, or "" if there isn't one.
func findDotnet8(root string) string {
	candidates := []string{filepath.Join(root, "dotnet")}
	if path, err := exec.LookPath("dotnet"); err == nil {
		candidates = append(candidates, path)
	}
	for _, dotnet := range candidates {
		out, err := run(nil, dotnet, "--list-sdks")
		if err != nil {
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "8.") {
				return dotnet
			}
		}
	}
	return ""
}
