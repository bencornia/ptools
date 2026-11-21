package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Extension is the VS Code extension's ID, and oldExtension the ID it had
// as p-lsp's.
const (
	Extension    = "bencornia.ptools"
	oldExtension = "bencornia.p-lsp"
)

// vscode checks that VS Code has this release's extension, installing it
// from the release to fix that.
func (d *Doctor) vscode() {
	code, err := exec.LookPath("code")
	if err != nil {
		d.skip("VS Code", "not installed")
		return
	}
	d.section("VS Code")
	out, err := run(nil, code, "--list-extensions", "--show-versions")
	if err != nil {
		d.bad(false, "code --list-extensions failed: %s", firstLine(out))
		return
	}
	installed := map[string]string{}
	for _, line := range strings.Fields(out) {
		if id, v, ok := strings.Cut(line, "@"); ok {
			installed[strings.ToLower(id)] = v
		}
	}
	if v, ok := installed[oldExtension]; ok {
		if d.bad(true, "the p-lsp extension, %s %s, is still installed, and conflicts with ptools'", oldExtension, v) {
			d.doing("uninstalling it")
			if err := runIn("", nil, code, "--uninstall-extension", oldExtension); err != nil {
				d.failed(err)
			}
		}
	}
	v, ok := installed[Extension]
	problem := fmt.Sprintf("the ptools extension, %s, isn't installed", Extension)
	switch {
	case ok && v == d.Version:
		d.good("the ptools extension, %s %s", Extension, v)
		return
	case ok:
		problem = fmt.Sprintf("the ptools extension, %s, is version %s, not %s", Extension, v, d.Version)
	}
	if !d.bad(true, "%s", problem) {
		return
	}
	vsix := fmt.Sprintf("%s/v%s/ptools-%s.vsix", d.Releases, d.Version, d.Version)
	d.doing("installing it from %s", vsix)
	tmp, err := os.MkdirTemp("", "plsp-doctor")
	if err != nil {
		d.failed(err)
		return
	}
	defer os.RemoveAll(tmp)
	path, err := download(vsix, tmp)
	if err == nil {
		err = runIn("", nil, code, "--install-extension", path, "--force")
	}
	if err != nil {
		d.failed(err)
	}
}
