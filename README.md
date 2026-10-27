# pfmt

A formatter for the [P programming language](https://github.com/p-org/P),
built on [tree-sitter-p](https://github.com/bencornia/tree-sitter-p).

Like `gofmt`, it keeps your code's shape: its tokens, the line breaks
between them, and comments. It normalizes the whitespace:

- Indentation: one level per block, a closing bracket lines up with the
  line that opened it, and a statement continued on the next line, or the
  statement after a brace-less `if`, `else`, `while` or `foreach`, is one
  level deeper.
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
pfmt -indent 4 Client.p    # indent with 4 spaces instead of 2
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
indent = { tab-width = 2, unit = "  " }
formatter = { command = "pfmt" }
auto-format = true
```

For 4-space indentation, use `unit = "    "` and
`formatter = { command = "pfmt", args = ["-indent", "4"] }`.

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
- [ ] `-d` to print diffs instead of formatted files
- [ ] `textDocument/formatting` in p-lsp, using the `format` package
- [ ] Editor setup for Neovim and VS Code
