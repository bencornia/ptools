# P LSP

An lsp written for the [P programming language](https://github.com/p-org/P).

## Building

The server uses a local checkout of [tree-sitter-p](https://github.com/bencornia/tree-sitter-p)
via a `replace` directive in `go.mod`, so clone it next to this repo first:

```sh
git clone https://github.com/bencornia/tree-sitter-p ../tree-sitter-p
go build -o bin/p-lsp .
go test ./...
```

Building requires cgo (a C compiler), since tree-sitter is a C library.

After regenerating the grammar, rebuild with `go build -a`. The Go binding
`#include`s `parser.c` from outside its package, so a plain `go build` can
reuse a stale cached parser.

## Running

The server speaks LSP over stdin/stdout. Point your editor at `bin/p-lsp` for
`*.p` files. Logs go to stderr; pass `-v 2` for more of them.

Saving a file that's listed in a `.pproj` type-checks its project with the
[P compiler](https://p-org.github.io/P/getstarted/install/), and its errors
appear alongside syntax errors. This needs `p` on your `PATH`; otherwise it's
off, and the log says so. To use a different command, pass
`-compiler "<command>"`, or `-compiler ""` to turn it off. Generated code goes
to a temporary directory, not your project.

`p` is a .NET tool, so if you installed .NET in your home directory, the
environment your editor starts the server in needs it too:

```sh
export DOTNET_ROOT="$HOME/.dotnet"
export PATH="$PATH:$HOME/.dotnet:$HOME/.dotnet/tools"
```

P 3.1.0 stops at the first type error, so errors are reported one at a time;
newer compilers report them all at once.

## TODO

### Server features

- [x] Syntax error diagnostics
- [x] Document outline (`textDocument/documentSymbol`)
- [x] Go to definition, across all `.p` files in the workspace
- [x] Read `.pproj` files: follow `<IncludeProject>` and only index listed `<PFile>` paths
- [x] Reload `.pproj` files and newly created `.p` files when they change on disk
- [x] Watch included projects outside the workspace (e.g. `../Common`) for changes,
      in editors that support LSP 3.17 relative watch patterns
- [x] Find a file's `.pproj` by searching parent directories when it's opened outside the workspace
- [x] Go to definition for tuple fields, and the P verifier's machine and event fields
- [x] Go to definition for fields in tuple values, like `(source = this)`, from the type their context expects
- [x] Find references
- [x] Narrower fallback for files in no `.pproj`: their example (under the `PSrc`/`PSpec`/`PTst` layout) or their own directory, not the whole workspace
- [x] Hover: a symbol's signature, doc comment, and file if declared elsewhere
- [x] Rename, refusing renames that collide with or capture another name
- [x] Workspace symbol search, with fuzzy matching
- [x] Type-check diagnostics from `p compile` on save
- [x] Verify type-check diagnostics against the real P compiler (P 3.1.0)
- [x] Completion: names in scope, fields after `.`, states after `goto`, and keywords
- [x] Semantic tokens: names colored by what they refer to, plus keywords, comments, strings and numbers
- [x] Folding (braces and comments) and selection ranges (expand selection by syntax node)
- [ ] Tests for `internal/server`
- [ ] Editor setup instructions (VS Code, Neovim)

### Grammar ([tree-sitter-p](https://github.com/bencornia/tree-sitter-p))

- [x] Fix `keys`/`values`/`sizeof` to take expressions
- [x] Fix foreign type declarations (`type Name;`)
- [x] Fix commas in `compose` module expressions
- [x] Fix `inflight` keyword and its precedence, and `sent` precedence
- [x] Fix `Proof` keyword capitalization
- [x] Fix foreign functions ending in `ensures` clauses
- [x] Add `scenario` declarations
- [x] Fix `rhs` field on comparison and `in` expressions
- [x] Start a `test/corpus`
- [ ] Remove `.git` from the Go module path, so p-lsp can depend on it without a `replace`
- [ ] Add `locals.scm` (definitions, references, scopes)
- [ ] Add `tags.scm`
- [ ] Grow `test/corpus` from the P repo's `Tst/` and `Tutorial/` programs
