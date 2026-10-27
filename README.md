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

## Running

The server speaks LSP over stdin/stdout. Point your editor at `bin/p-lsp` for
`*.p` files. Logs go to stderr; pass `-v 2` for more of them.

Currently supported:

- Syntax error diagnostics
- Document outline (`textDocument/documentSymbol`)
