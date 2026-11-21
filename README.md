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

```sh
go build -o ~/.local/bin/ ./cmd/...
```

That builds `plsp`, `prdb`, `pfmt` and `pdoc` into `~/.local/bin`, which
the editor setups expect to be on your `PATH`. Building requires cgo (a C compiler),
since tree-sitter is a C library.

Each takes `--help`, as do plsp's and prdb's subcommands, and
`completion bash`, `zsh`, `fish` or `powershell` prints a shell completion
script, as in `source <(plsp completion bash)`. Long flags take two dashes,
as in `--compiler`; single letters take one, as in `pfmt -w`.

Type checking, running tests and replaying schedules need the
[P compiler](https://p-org.github.io/P/getstarted/install/), `p`. plsp's
and prdb's READMEs say where they look for it.

## VS Code

```sh
cd editors/vscode
npm install
npx @vscode/vsce package --skip-license
code --install-extension ptools-0.7.0.vsix
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
