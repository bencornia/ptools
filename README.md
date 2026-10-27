# P LSP

An lsp written for the [P programming language](https://github.com/p-org/P).

## Building

```sh
go build -o bin/p-lsp .
go test ./...
```

Building requires cgo (a C compiler), since tree-sitter is a C library.

While the grammar's repository is private, Go has to fetch it directly over
SSH rather than through the public module proxy:

```sh
go env -w 'GOPRIVATE=github.com/bencornia/*'
git config --global url."git@github.com:".insteadOf "https://github.com/"
```

The grammar, [tree-sitter-p](https://github.com/bencornia/tree-sitter-p), is
an ordinary Go dependency. To work on both at once, use a local checkout
through a Go workspace, which `.gitignore` keeps out of commits:

```sh
git clone https://github.com/bencornia/tree-sitter-p ../tree-sitter-p
go work init . ../tree-sitter-p
```

After regenerating the grammar, rebuild with `go build -a`. The Go binding
`#include`s `parser.c` from outside its package, so a plain `go build` can
reuse a stale cached parser.

## Running

The server speaks LSP over stdin/stdout. The editor setups below expect
`p-lsp` on your `PATH`, so build it there, for example:

```sh
go build -o ~/.local/bin/p-lsp .
```

Logs go to stderr; pass `-v 2` for more of them.

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

P 3.0 stops at the first error, so errors are reported one at a time; P 3.1
reports several at once. Both versions' output formats are understood.

## Editor setup

Open the folder that holds your `.pproj` files (or a parent of them) as the
workspace. A file opened on its own still works: the server searches its
parent directories for a `.pproj` that lists it.

### Helix

Helix doesn't use semantic tokens, so colors come from the tree-sitter-p
grammar. Add this to `~/.config/helix/languages.toml`:

```toml
# Only fetch and build P's grammar with `hx --grammar`; Helix's own
# grammars are already built.
use-grammars = { only = ["p"] }

[language-server.p-lsp]
command = "p-lsp"

[[language]]
name = "p"
scope = "source.p"
file-types = ["p"]
comment-token = "//"
block-comment-tokens = { start = "/*", end = "*/" }
indent = { tab-width = 4, unit = "    " }
language-servers = ["p-lsp"]
# Format with pfmt, through the server, on save.
auto-format = true

[[grammar]]
name = "p"
source = { git = "https://github.com/bencornia/tree-sitter-p", rev = "899bd465462a536d89f8fb583dccdd70962eca51" }
```

Then build the grammar and install its highlight queries. Helix doesn't
create the grammars directory itself:

```sh
mkdir -p ~/.config/helix/runtime/grammars ~/.config/helix/runtime/queries/p
hx --grammar fetch && hx --grammar build
cp ~/.config/helix/runtime/grammars/sources/p/queries/*.scm ~/.config/helix/runtime/queries/p/
hx --health p
```

To pick up grammar changes, update `rev` and run the last three commands
again. `space d` lists errors (syntax errors as you type, type errors on
save), and `gd`, `gr`, `space k`, `space r`, `space s` and `space S` are
definition, references, hover, rename and symbols.

### Neovim

For Neovim 0.11 or later. Neovim detects `.p` files as Pascal or Progress,
so claim the extension first. With no tree-sitter parser for P, the
server's semantic tokens do the highlighting.

```lua
vim.filetype.add({ extension = { p = 'p' } })

vim.lsp.config('p_lsp', {
  cmd = { 'p-lsp' },
  filetypes = { 'p' },
  root_markers = { '.git' },
})
vim.lsp.enable('p_lsp')

-- Optional: fold with the server's folding ranges.
vim.api.nvim_create_autocmd('FileType', {
  pattern = 'p',
  callback = function()
    vim.wo.foldmethod = 'expr'
    vim.wo.foldexpr = 'v:lua.vim.lsp.foldexpr()'
  end,
})

-- Optional: format with pfmt on save.
vim.api.nvim_create_autocmd('BufWritePre', {
  pattern = '*.p',
  callback = function() vim.lsp.buf.format() end,
})
```

### VS Code

VS Code needs an extension to start a language server; a minimal one is in
`editors/vscode`. Package and install it:

```sh
cd editors/vscode
npm install
npx @vscode/vsce package --skip-license
code --install-extension p-lsp-0.1.0.vsix
```

It highlights with the server's semantic tokens. Set `p-lsp.path` if
`p-lsp` isn't on the `PATH` VS Code sees, and `p-lsp.args` to pass flags
such as `["-compiler", ""]`. Format Document formats with pfmt; to format
on save, add `"[p]": { "editor.formatOnSave": true }` to `settings.json`.

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
- [x] Quick fixes: add the trailing comma a one-field tuple needs, for `(a = 1)` (a syntax error) and `(1)` where a tuple is expected (a type error the compiler only reports on save)
- [x] Quick fix: add a missing `;` (or other token tree-sitter finds missing), including between top-level declarations
- [x] Quick fix: declare an undefined event, with its payload type taken from how it's handled or sent
- [x] Quick fix: change a misspelled name to the closest name in scope (variables, functions, types, states, modules, fields, events)
- [x] Signature help: a function's parameters, a machine's start parameter after `new`, or an event's payload type in `send`, `raise` and `announce`, while typing arguments
- [x] Formatting with [pfmt](https://github.com/bencornia/pfmt), indenting by the editor's tab size
- [x] Merge the full-tree passes behind diagnostics (tuple checks, undefined events and names, empty receives) into one walk, to speed up large files
- [x] Tests for `internal/server`, and an end-to-end test of the binary over stdio
- [x] Editor setup instructions (Helix, Neovim, VS Code)

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
- [x] Remove `.git` from the Go module path, so p-lsp can depend on it without a `replace`
- [x] Add `locals.scm` (definitions, references, scopes)
- [x] Add `tags.scm`
- [x] Grow `test/corpus` from the P repo's `Tst/` and `Tutorial/` programs
- [x] Add `textobjects.scm` (functions and handlers, machines, parameters, comments, tests) and `indents.scm`
