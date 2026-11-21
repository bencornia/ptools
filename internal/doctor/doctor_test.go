package doctor

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// setup gives the test a home directory of its own, and a PATH with only
// the commands the doctor runs that tests don't fake, and returns the home
// directory and the directory to put fakes in, which is on the PATH.
func setup(t *testing.T) (home, bin string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fakes are shell scripts")
	}
	home = t.TempDir()
	bin = filepath.Join(home, "bin")
	sys := filepath.Join(home, "sys")
	for _, dir := range []string{bin, sys} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range []string{"sh", "bash", "cat", "mkdir", "chmod", "rm", "head", "grep", "git", "cc", "as", "ld"} {
		if path, err := exec.LookPath(tool); err == nil {
			if err := os.Symlink(path, filepath.Join(sys, tool)); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+sys)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DOTNET_ROOT", "")
	return home, bin
}

// script writes a shell script to path.
func script(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func newDoctor(fix bool) (*Doctor, *strings.Builder) {
	var out strings.Builder
	d := New(&out, fix)
	d.Version = "1.2.3"
	return d, &out
}

func expect(t *testing.T, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("output doesn't have %q:\n%s", w, out)
		}
	}
}

func TestTools(t *testing.T) {
	_, bin := setup(t)
	for _, name := range Tools {
		v := "1.2.3"
		if name == "pfmt" {
			v = "1.0.0"
		}
		script(t, filepath.Join(bin, name), "echo "+v+"\n")
	}
	d, out := newDoctor(false)
	d.tools()
	expect(t, out.String(), "pfmt, at ~/bin/pfmt, is version 1.0.0, not 1.2.3", "install.sh | sh")
	if d.problems != 1 || d.fixable != 0 {
		t.Errorf("problems %d, fixable %d; want 1 and 0", d.problems, d.fixable)
	}

	script(t, filepath.Join(bin, "pfmt"), "echo 1.2.3\n")
	d, out = newDoctor(false)
	d.tools()
	expect(t, out.String(), "✓ plsp, prdb, pfmt and pdoc 1.2.3, in ~/bin")
	if d.problems != 0 {
		t.Errorf("problems %d; want 0:\n%s", d.problems, out)
	}
}

func TestPFound(t *testing.T) {
	home, _ := setup(t)
	script(t, filepath.Join(home, ".dotnet", "tools", "p"), "echo 'P version 3.1.0.0'\n")
	d, out := newDoctor(false)
	d.pCompiler()
	expect(t, out.String(), "✓ P 3.1.0, at ~/.dotnet/tools/p", "your shell doesn't", `export PATH="$PATH:$HOME/.dotnet/tools"`)
	if d.problems != 0 {
		t.Errorf("problems %d; want 0", d.problems)
	}
}

// fakeDotnetInstall is a dotnet-install.sh that installs a dotnet that
// installs a p.
const fakeDotnetInstall = `while [ $# -gt 0 ]; do
	case $1 in --install-dir) dir=$2; shift;; --channel) [ "$2" = 8.0 ] || exit 3; shift;; esac
	shift
done
mkdir -p "$dir"
cat > "$dir/dotnet" <<'EOF'
#!/bin/sh
case "$1 $2 $3 $4" in
"--list-sdks   ") echo "8.0.100 [$DOTNET_ROOT/sdk]";;
"tool install --global P")
	mkdir -p "$HOME/.dotnet/tools"
	printf '#!/bin/sh\n[ -n "$DOTNET_ROOT" ] || exit 1\necho "P version 3.1.0.0"\n' > "$HOME/.dotnet/tools/p"
	chmod +x "$HOME/.dotnet/tools/p";;
*) exit 2;;
esac
EOF
chmod +x "$dir/dotnet"
`

func TestPInstall(t *testing.T) {
	setup(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dotnet-install.sh" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(fakeDotnetInstall))
	}))
	defer server.Close()

	d, out := newDoctor(false)
	d.pCompiler()
	expect(t, out.String(), "✗ P isn't installed")
	if d.problems != 1 || d.fixable != 1 {
		t.Errorf("problems %d, fixable %d; want 1 and 1", d.problems, d.fixable)
	}

	d, out = newDoctor(true)
	d.DotnetInstall = server.URL + "/dotnet-install.sh"
	d.pCompiler()
	expect(t, out.String(),
		"→ installing .NET 8 into ~/.dotnet",
		"→ installing P, with dotnet tool install --global P",
		"✓ P 3.1.0, at ~/.dotnet/tools/p",
		`export DOTNET_ROOT="$HOME/.dotnet" PATH="$PATH:$HOME/.dotnet:$HOME/.dotnet/tools"`)
	if d.problems != 0 {
		t.Errorf("problems %d; want 0:\n%s", d.problems, out)
	}
}

func readLanguages(t *testing.T, file string) helixLanguages {
	t.Helper()
	var config helixLanguages
	if _, err := toml.DecodeFile(file, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func TestHelixConfig(t *testing.T) {
	home, _ := setup(t)
	file := filepath.Join(home, ".config", "helix", "languages.toml")
	mine := "[[language]]\nname = \"go\"\nauto-format = true"
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}

	d, out := newDoctor(false)
	d.helixConfig(file)
	expect(t, out.String(), "✗ languages.toml doesn't set up P")
	if d.problems != 1 || d.fixable != 1 {
		t.Errorf("problems %d, fixable %d; want 1 and 1:\n%s", d.problems, d.fixable, out)
	}

	d, out = newDoctor(true)
	d.helixConfig(file)
	expect(t, out.String(), "changed ~/.config/helix/languages.toml: added P, with plsp", "kept the old one")
	if d.problems != 0 {
		t.Errorf("problems %d; want 0:\n%s", d.problems, out)
	}
	if backup, _ := os.ReadFile(file + ".bak"); string(backup) != mine {
		t.Errorf("backup is %q; want %q", backup, mine)
	}
	text, _ := os.ReadFile(file)
	if !strings.HasPrefix(string(text), mine+"\n\n# P, with plsp") {
		t.Errorf("languages.toml doesn't keep what it had:\n%s", text)
	}
	config := readLanguages(t, file)
	if config.LanguageServer["plsp"].Command != "plsp" || len(config.Language) != 2 || len(config.Grammar) != 1 ||
		config.Grammar[0].Source.Rev != d.GrammarRev {
		t.Errorf("languages.toml doesn't set up P:\n%s", text)
	}

	// An older pin is updated.
	old := strings.Replace(string(text), d.GrammarRev, "0123456789abcdef0123456789abcdef01234567", 1)
	if err := os.WriteFile(file, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	d, out = newDoctor(true)
	d.helixConfig(file)
	expect(t, out.String(), "✓ languages.toml sets up P with plsp", "pins P's grammar to 0123456, not", "pinned P's grammar to")
	if text, _ := os.ReadFile(file); strings.Contains(string(text), "0123456789") {
		t.Errorf("the pin wasn't updated:\n%s", text)
	}

	// And then there's nothing to do.
	d, out = newDoctor(true)
	d.helixConfig(file)
	if strings.Contains(out.String(), "✗") || strings.Contains(out.String(), "→") {
		t.Errorf("changed a languages.toml that was set up:\n%s", out)
	}
}

func TestHelixConfigOtherServer(t *testing.T) {
	home, _ := setup(t)
	file := filepath.Join(home, "languages.toml")
	text := "[language-server.mine]\ncommand = \"other\"\n\n[[language]]\nname = \"p\"\nlanguage-servers = [\"mine\"]\n"
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	d, out := newDoctor(true)
	d.helixConfig(file)
	expect(t, out.String(), "P language doesn't use plsp", "doesn't say where to get P's grammar", "added P's grammar")
	if d.problems != 1 {
		t.Errorf("problems %d; want 1:\n%s", d.problems, out)
	}
}

// grammarRepo makes a git repository for a grammar, and returns it and its
// commit.
func grammarRepo(t *testing.T, dir string) (string, string) {
	t.Helper()
	files := map[string]string{
		"src/parser.c":           "const void *tree_sitter_p(void) { return 0; }\n",
		"queries/highlights.scm": "(comment) @comment\n",
		"queries/indents.scm":    "(block) @indent\n",
	}
	for name, text := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"commit", "-q", "-m", "grammar"}} {
		if err := runIn(dir, env, "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	rev, err := run(nil, "git", "-C", dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(rev)
	}
	return dir, strings.TrimSpace(rev)
}

func TestHelixGrammar(t *testing.T) {
	home, _ := setup(t)
	for _, tool := range []string{"git", "cc"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s", tool)
		}
	}
	repo, rev := grammarRepo(t, filepath.Join(home, "tree-sitter-p"))
	runtimeDir := filepath.Join(home, "runtime")

	d, out := newDoctor(false)
	d.GrammarRepo, d.GrammarRev = repo, rev
	d.helixGrammar(runtimeDir)
	expect(t, out.String(), "✗ P's grammar isn't built")

	d, out = newDoctor(true)
	d.GrammarRepo, d.GrammarRev = repo, rev
	d.helixGrammar(runtimeDir)
	expect(t, out.String(), "→ fetching tree-sitter-p", "✗ its queries aren't installed", "→ copying them")
	if d.problems != 0 {
		t.Fatalf("problems %d; want 0:\n%s", d.problems, out)
	}
	if !exists(filepath.Join(runtimeDir, "grammars", "p.so")) {
		t.Error("p.so wasn't built")
	}
	if q, _ := os.ReadFile(filepath.Join(runtimeDir, "queries", "p", "highlights.scm")); string(q) != "(comment) @comment\n" {
		t.Errorf("highlights.scm is %q", q)
	}

	d, out = newDoctor(false)
	d.GrammarRepo, d.GrammarRev = repo, rev
	d.helixGrammar(runtimeDir)
	expect(t, out.String(), "✓ P's grammar is built, at "+rev[:7], "✓ its queries are installed")
	if d.problems != 0 {
		t.Errorf("problems %d; want 0:\n%s", d.problems, out)
	}
}

// fakeCode is a code command that keeps its extensions in $HOME/extensions.
const fakeCode = `list="$HOME/extensions"
touch() { [ -f "$list" ] || : > "$list"; }
touch
case $1 in
--list-extensions) cat "$list";;
--install-extension) [ "$(cat "$2")" = vsix ] || exit 1; echo bencornia.ptools@1.2.3 >> "$list";;
--uninstall-extension) : > "$list.new"; while read -r e; do case $e in "$2"@*) ;; *) echo "$e" >> "$list.new";; esac; done < "$list"; cat "$list.new" > "$list";;
esac
`

func TestVSCode(t *testing.T) {
	home, bin := setup(t)
	script(t, filepath.Join(bin, "code"), fakeCode)
	if err := os.WriteFile(filepath.Join(home, "extensions"), []byte("bencornia.p-lsp@0.6.0\nother.ext@1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.2.3/ptools-1.2.3.vsix" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("vsix"))
	}))
	defer server.Close()

	d, out := newDoctor(false)
	d.vscode()
	expect(t, out.String(), "the p-lsp extension, bencornia.p-lsp 0.6.0, is still installed", "bencornia.ptools, isn't installed")
	if d.problems != 2 || d.fixable != 2 {
		t.Errorf("problems %d, fixable %d; want 2 and 2", d.problems, d.fixable)
	}

	d, out = newDoctor(true)
	d.Releases = server.URL
	d.vscode()
	if d.problems != 0 {
		t.Fatalf("problems %d; want 0:\n%s", d.problems, out)
	}
	if list, _ := os.ReadFile(filepath.Join(home, "extensions")); string(list) != "other.ext@1.0.0\nbencornia.ptools@1.2.3\n" {
		t.Errorf("extensions are %q", list)
	}

	d, out = newDoctor(false)
	d.vscode()
	expect(t, out.String(), "✓ the ptools extension, bencornia.ptools 1.2.3")
}

func TestHelixNotInstalled(t *testing.T) {
	home, _ := setup(t)
	dir := filepath.Join(home, ".config", "helix")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	d, out := newDoctor(true)
	d.helix()
	expect(t, out.String(), "Helix: not installed")
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("set up Helix's leftover configuration: %v", entries)
	}
}

func TestVSCodeApp(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("VS Code's app is only looked in on macOS")
	}
	home, _ := setup(t)
	script(t, filepath.Join(home, "Applications", "Visual Studio Code.app", "Contents", "Resources", "app", "bin", "code"),
		"echo bencornia.ptools@1.2.3\n")
	d, out := newDoctor(false)
	d.vscode()
	expect(t, out.String(), "the code command isn't on your PATH", "✓ the ptools extension, bencornia.ptools 1.2.3")
}

// fakeNvim is an nvim that gives P files the p filetype and starts plsp
// only when ptools.lua is in its plugin directory, as Neovim would with
// the plugin the doctor adds.
const fakeNvim = `case $1 in
--version) echo "NVIM v${FAKE_NVIM_VERSION:-0.12.6}"; exit;;
esac
config="$HOME/.config/nvim"
if [ -f "$config/plugin/ptools.lua" ] && head -1 "$config/plugin/ptools.lua" | grep -q 'from ptools'; then
	printf 'config=%s\nfiletype=p\nclient=plsp\tplsp\n' "$config" > "$PLSP_DOCTOR_OUT"
else
	printf 'config=%s\nfiletype=progress\n' "$config" > "$PLSP_DOCTOR_OUT"
fi
`

func TestNeovim(t *testing.T) {
	home, bin := setup(t)
	script(t, filepath.Join(bin, "nvim"), fakeNvim)
	plugin := filepath.Join(home, ".config", "nvim", "plugin", "ptools.lua")

	d, out := newDoctor(false)
	d.neovim()
	expect(t, out.String(), "✗ Neovim gives .p files the filetype progress, not p")
	if d.problems != 1 || d.fixable != 1 {
		t.Errorf("problems %d, fixable %d; want 1 and 1", d.problems, d.fixable)
	}

	d, out = newDoctor(true)
	d.neovim()
	expect(t, out.String(), "→ adding ~/.config/nvim/plugin/ptools.lua")
	if d.problems != 0 {
		t.Errorf("problems %d; want 0:\n%s", d.problems, out)
	}
	if text, _ := os.ReadFile(plugin); string(text) != neovimPlugin {
		t.Errorf("ptools.lua is %q", text)
	}

	d, out = newDoctor(false)
	d.neovim()
	expect(t, out.String(), "✓ Neovim 0.12 starts plsp for P files")
}

func TestNeovimLeavesOtherPlugins(t *testing.T) {
	home, bin := setup(t)
	script(t, filepath.Join(bin, "nvim"), fakeNvim)
	plugin := filepath.Join(home, ".config", "nvim", "plugin", "ptools.lua")
	if err := os.MkdirAll(filepath.Dir(plugin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plugin, []byte("-- mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, out := newDoctor(true)
	d.neovim()
	expect(t, out.String(), "isn't the doctor's to replace")
	if text, _ := os.ReadFile(plugin); string(text) != "-- mine\n" {
		t.Errorf("replaced the user's ptools.lua with %q", text)
	}
	if d.problems != 1 || d.fixable != 0 {
		t.Errorf("problems %d, fixable %d; want 1 and 0", d.problems, d.fixable)
	}
}

func TestNeovimTooOld(t *testing.T) {
	_, bin := setup(t)
	script(t, filepath.Join(bin, "nvim"), fakeNvim)
	t.Setenv("FAKE_NVIM_VERSION", "0.10.4")
	d, out := newDoctor(true)
	d.neovim()
	expect(t, out.String(), "Neovim is version 0.10, and plsp's setup needs 0.11 or later")
	if d.problems != 1 || d.fixable != 0 {
		t.Errorf("problems %d, fixable %d; want 1 and 0", d.problems, d.fixable)
	}
}
