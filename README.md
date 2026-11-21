# ptools

Tools for the [P programming language](https://github.com/p-org/P), built on
the [tree-sitter-p](https://github.com/bencornia/tree-sitter-p) grammar:

- [plsp](cmd/plsp), a language server: diagnostics, type checking with the
  P compiler on save, navigation, rename, completion, lint checks, and test
  case lenses.
- [prdb](cmd/prdb), a replay debugger for PChecker's traces, over the Debug
  Adapter Protocol, and space-time diagrams of them.
- [pfmt](cmd/pfmt), a gofmt-style formatter. plsp formats with it too.
- [pdoc](cmd/pdoc), which writes reference documentation for P projects as
  AsciiDoc, organized like Go's package documentation.
- [editors/vscode](editors/vscode), the P extension for VS Code, which runs
  plsp and prdb.

## Installing

On Linux or macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/bencornia/ptools/main/install.sh | sh
```

That installs `plsp`, `prdb`, `pfmt` and `pdoc` from the latest release
into `~/.local/bin`, and then runs `plsp doctor --fix`, which sets up the
rest:

- **The P compiler**, `p`, which type checking, running tests and
  replaying schedules need. The doctor installs it with
  `dotnet tool install --global P`, after installing .NET 8 into
  `~/.dotnet` with Microsoft's
  [dotnet-install.sh](https://learn.microsoft.com/dotnet/core/tools/dotnet-install-script)
  if you don't have it. Neither needs root.
- **Helix**, if you have it: it adds P to `languages.toml`, keeping the old
  one as `languages.toml.bak`, and builds the grammar and installs its
  queries. Building needs `git` and a C compiler.
- **VS Code**, if you have it: it installs the release's extension.

Run `plsp doctor` again at any time to check the setup, as after upgrading,
and `plsp doctor --fix` to fix what it finds. It doesn't change your
shell's profile, so if `~/.local/bin` isn't on your `PATH`, it says what to
add. To install somewhere else, set `PTOOLS_BIN`; to install another
release, set `PTOOLS_VERSION`, as in `PTOOLS_VERSION=0.8.0`; and to only
check, set `PTOOLS_FIX=0`.

On Windows, download `ptools_windows_amd64.zip` from the
[latest release](https://github.com/bencornia/ptools/releases/latest), and
put its programs on your `PATH`.

### From source

```sh
go install github.com/bencornia/ptools/cmd/...@latest
plsp doctor --fix
```

or, in a checkout, `go build -o ~/.local/bin/ ./cmd/...`. Building
requires cgo (a C compiler), since tree-sitter is a C library.

### Using them

Each takes `--help`, as do plsp's and prdb's subcommands, and
`completion bash`, `zsh`, `fish` or `powershell` prints a shell completion
script, as in `source <(plsp completion bash)`. Long flags take two dashes,
as in `--compiler`; single letters take one, as in `pfmt -w`.

plsp's and prdb's READMEs say where they look for the P compiler.

## VS Code

`plsp doctor --fix` installs the extension from the release. To build it
yourself:

```sh
cd editors/vscode
npm install
npx @vscode/vsce package --skip-license
code --install-extension ptools-*.vsix
```

Set `plsp.path` or `prdb.path` if the binaries aren't on the `PATH` VS
Code sees. [plsp's README](cmd/plsp/README.md#vs-code) describes what the
extension does, and [prdb's](cmd/prdb/README.md#vs-code) the debugger.

## Developing

```sh
go test ./...
```

Some of prdb's tests run the P compiler, and are skipped without it.

The grammar is an ordinary Go dependency. To work on it at the same time,
use a local checkout through a Go workspace, which `.gitignore` keeps out of
commits:

```sh
git clone https://github.com/bencornia/tree-sitter-p ../tree-sitter-p
go work init . ../tree-sitter-p
```

After regenerating the grammar, rebuild with `go build -a`. The Go binding
`#include`s `parser.c` from outside its package, so a plain `go build` can
reuse a stale cached parser.

## Releasing

Every command reports one version, the ptools release. To release, set it
in `internal/version` and in `editors/vscode/package.json` (and
`package-lock.json`), commit, and push an annotated tag of `v` and the
version, as in `v0.8.0`:

```sh
git tag -a v0.8.0 -m "$(cat notes.md)"
git push origin v0.8.0
```

The [release workflow](.github/workflows/release.yml) then tests and builds
the tools for Linux and macOS (amd64 and arm64) and Windows (amd64),
packages the extension, and attaches them and a `checksums.txt` to the
tag's release, which it makes with the tag's message as its notes.
`install.sh` installs from the latest release. To try the builds without
releasing, run the workflow from the Actions tab.

When go.mod's tree-sitter-p changes, set `version.Grammar` to the new
commit, which `plsp doctor` pins Helix's grammar to; a test checks that
they match.

## License

MIT
