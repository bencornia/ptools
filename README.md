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

## TODO

### Server features

- [x] Syntax error diagnostics
- [x] Document outline (`textDocument/documentSymbol`)
- [ ] Go to definition
- [ ] Find references
- [ ] Hover (show a symbol's declaration)
- [ ] Rename
- [ ] Workspace symbol search
- [ ] Type-check diagnostics from `p compile` on save
- [ ] Completion of names in scope
- [ ] Semantic tokens
- [ ] Folding and selection ranges
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
