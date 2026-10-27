# pfmt

A formatter for the [P programming language](https://github.com/p-org/P),
built on [tree-sitter-p](https://github.com/bencornia/tree-sitter-p).

Like `gofmt`, it keeps your code's shape: its tokens, the line breaks
between them, and comments. It normalizes the whitespace:

- Indentation: 4 spaces per level by default, one level per block. A
  closing bracket lines up with the line that opened it, and a statement
  continued on the next line, or the statement after a brace-less `if`,
  `else`, `while` or `foreach`, is one level deeper.
- Spacing on a line: around operators, after commas, before `{`, after the
  `:` in `x: int`, none between a function and its `(`, and one between a
  keyword and its `(`, as in `if (x)`.
- Blank lines: runs of them become one, with none at the start of the file.
- Trailing whitespace is removed, and the file ends with one newline.

Trailing comments keep their alignment.

pfmt refuses code with syntax errors. It also checks its own output: if
formatting would change anything but whitespace, it reports an error instead.

## Installing

```sh
go build -o ~/.local/bin/pfmt .
```

Building requires cgo (a C compiler), since tree-sitter is a C library.
While the grammar's repository is private, Go has to fetch it over SSH:

```sh
go env -w 'GOPRIVATE=github.com/bencornia/*'
git config --global url."git@github.com:".insteadOf "https://github.com/"
```

## Usage

```sh
pfmt < Client.p            # format standard input to standard output
pfmt PSrc/Client.p         # print the formatted file
pfmt -w .                  # format every .p file under the directory in place
pfmt -l .                  # list the files whose formatting differs
pfmt -d .                  # print diffs of what formatting would change
pfmt -indent 2 Client.p    # indent with 2 spaces instead of 4
```

It exits with status 1 if a file couldn't be formatted, and 2 for bad usage
or unformattable standard input.

## Helix

Add a formatter to the `p` language in `~/.config/helix/languages.toml`, and
match its indentation:

```toml
[[language]]
name = "p"
# ...
indent = { tab-width = 4, unit = "    " }
formatter = { command = "pfmt" }
auto-format = true
```

For another width, change `unit` and pass it to pfmt too, as in
`formatter = { command = "pfmt", args = ["-indent", "2"] }`.

## Neovim and VS Code

[p-lsp](https://github.com/bencornia/p-lsp) formats with pfmt, so with p-lsp
set up, the editor's own formatting commands work, using the editor's tab
size as the indent width.

In Neovim, format with `vim.lsp.buf.format()`, or on every save:

```lua
vim.api.nvim_create_autocmd('BufWritePre', {
  pattern = '*.p',
  callback = function() vim.lsp.buf.format() end,
})
```

Without p-lsp, `gq` can run pfmt directly:

```lua
vim.api.nvim_create_autocmd('FileType', {
  pattern = 'p',
  callback = function() vim.bo.formatprg = 'pfmt' end,
})
```

In VS Code, use Format Document, or format on save with this in
`settings.json`:

```json
"[p]": {
  "editor.formatOnSave": true,
  "editor.tabSize": 4
}
```

## Developing

```sh
go test ./...
PFMT_CORPUS=path/to/P go test ./format -run Corpus   # format a whole repo of P code
```

The corpus test checks that every file formats, and that formatting again
changes nothing. All of github.com/p-org/P passes.

## TODO

- [x] Format whitespace: indentation, spacing on a line, blank lines, file end
- [x] Keep line breaks and comments, and trailing comments' alignment
- [x] Refuse code with syntax errors, and check that output has the same tokens
- [x] `pfmt` command: stdin, files and directories, `-w`, `-l`, `-indent`
- [x] Tests, and a corpus test over the P repo
- [x] `-d` to print diffs instead of formatted files
- [x] `textDocument/formatting` in p-lsp, using the `format` package
- [x] Editor setup for Neovim and VS Code
