package doctor

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// neovimProbe runs in Neovim, with the user's configuration, on a P file,
// and writes what it finds to $PLSP_DOCTOR_OUT: Neovim's configuration
// directory, the file's filetype, and the language servers that attach.
//
//go:embed neovim_probe.lua
var neovimProbe string

// neovimPlugin is the plugin the doctor adds to set Neovim up, as plsp's
// README does.
//
//go:embed neovim_plugin.lua
var neovimPlugin string

// neovimPluginMark starts neovimPlugin, so the doctor knows the plugin is
// its own to replace.
const neovimPluginMark = "-- P, with plsp from ptools"

// neovim checks that Neovim gives P files the p filetype and starts plsp
// for them, by opening one in Neovim with the user's configuration. To fix
// that, it adds a plugin to the configuration directory, leaving the
// user's own configuration alone.
func (d *Doctor) neovim() {
	nvim, err := exec.LookPath("nvim")
	if err != nil {
		d.skip("Neovim", "not installed")
		return
	}
	d.section("Neovim")
	out, _ := run(nil, nvim, "--version")
	m := regexp.MustCompile(`NVIM v(\d+)\.(\d+)`).FindStringSubmatch(out)
	if m == nil {
		d.bad(false, "can't tell Neovim's version from nvim --version: %s", firstLine(out))
		return
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if major == 0 && minor < 11 {
		d.bad(false, "Neovim is version %s.%s, and plsp's setup needs 0.11 or later, for vim.lsp.config", m[1], m[2])
		return
	}
	probe, err := probeNeovim(nvim)
	if err != nil {
		d.bad(false, "can't open a P file in Neovim: %v", err)
		return
	}
	problem := probe.problem()
	if problem == "" {
		d.good("Neovim %s.%s starts plsp for P files", m[1], m[2])
		return
	}
	plugin := filepath.Join(probe.config, "plugin", "ptools.lua")
	text, err := os.ReadFile(plugin)
	mine := err == nil && strings.HasPrefix(string(text), neovimPluginMark)
	if err == nil && !mine {
		d.bad(false, "%s, and %s isn't the doctor's to replace", problem, tilde(plugin))
		return
	}
	if mine && string(text) == neovimPlugin {
		// It's set up as the doctor would, so something else is wrong.
		d.bad(false, "%s, though %s sets it up. Check that your configuration doesn't override it", problem, tilde(plugin))
		return
	}
	if !d.bad(true, "%s", problem) {
		return
	}
	d.doing("adding %s, which sets up P as plsp's README does", tilde(plugin))
	if err := os.MkdirAll(filepath.Dir(plugin), 0o755); err != nil {
		d.failed(err)
		return
	}
	if err := os.WriteFile(plugin, []byte(neovimPlugin), 0o644); err != nil {
		d.failed(err)
		return
	}
	probe, err = probeNeovim(nvim)
	if err == nil && probe.problem() != "" {
		err = fmt.Errorf("%s, even so. Check that your configuration doesn't override it", probe.problem())
	}
	if err != nil {
		d.failed(err)
	}
}

// neovimFound is what neovimProbe found.
type neovimFound struct {
	config, filetype string
	clients          [][2]string // names and commands
}

func (f neovimFound) problem() string {
	if f.filetype != "p" {
		what := "no filetype"
		if f.filetype != "" {
			what = "the filetype " + f.filetype
		}
		return "Neovim gives .p files " + what + ", not p"
	}
	var others []string
	for _, c := range f.clients {
		if filepath.Base(c[1]) == "plsp" {
			return ""
		}
		others = append(others, c[0])
	}
	if len(others) > 0 {
		return "Neovim starts " + strings.Join(others, ", ") + " for P files, but not plsp"
	}
	return "Neovim doesn't start plsp for P files"
}

// probeNeovim opens a P file in nvim, headless, and reports what it found.
func probeNeovim(nvim string) (neovimFound, error) {
	var found neovimFound
	dir, err := os.MkdirTemp("", "plsp-doctor")
	if err != nil {
		return found, err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "Doctor.p")
	script := filepath.Join(dir, "probe.lua")
	result := filepath.Join(dir, "result")
	if err := os.WriteFile(file, []byte("machine Main { start state Init {} }\n"), 0o644); err != nil {
		return found, err
	}
	if err := os.WriteFile(script, []byte(neovimProbe), 0o644); err != nil {
		return found, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nvim, "--headless", "-n", "-i", "NONE", file, "-c", "luafile "+script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PLSP_DOCTOR_OUT="+result)
	out, err := cmd.CombinedOutput()
	text, readErr := os.ReadFile(result)
	if readErr != nil {
		if ctx.Err() != nil {
			return found, fmt.Errorf("Neovim didn't finish in 30s")
		}
		if err == nil {
			err = readErr
		}
		return found, fmt.Errorf("%v%s", err, indent(out))
	}
	for _, line := range strings.Split(string(text), "\n") {
		key, value, _ := strings.Cut(line, "=")
		switch key {
		case "config":
			found.config = value
		case "filetype":
			found.filetype = value
		case "client":
			name, command, _ := strings.Cut(value, "\t")
			found.clients = append(found.clients, [2]string{name, command})
		}
	}
	if found.config == "" {
		return found, fmt.Errorf("Neovim didn't say where its configuration is")
	}
	return found, nil
}
