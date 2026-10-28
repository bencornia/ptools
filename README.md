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

`p` is a .NET tool. An editor started from a desktop menu often doesn't
have the `PATH` your shell profile sets, so if `p` isn't on the `PATH`,
p-lsp looks where `dotnet tool install --global P` puts it,
`~/.dotnet/tools/p`, and runs it with `DOTNET_ROOT` set to `~/.dotnet` if
.NET is there. If yours is somewhere else, the environment your editor
starts the server in needs it:

```sh
export DOTNET_ROOT="$HOME/.dotnet"
export PATH="$PATH:$HOME/.dotnet:$HOME/.dotnet/tools"
```

P 3.0 stops at the first error, so errors are reported one at a time; P 3.1
reports several at once. Both versions' output formats are understood.

## Naming style

p-lsp warns about declared names that don't follow a naming style, and
offers to rename each one, along with everything that refers to it, unless
the new name would clash with another. The default style:

| Declaration | Case | Example |
|---|---|---|
| Types and enums | TitleCase | `type Lookup`, `enum Language` |
| Machines, specs, interfaces, states, events, modules | TitleCase | `Client`, `Serving`, `LookupDone` |
| Enum constants | SCREAMING_CASE | `ENGLISH`, `NOT_FOUND` |
| Functions | snake_case | `pick_language` |
| Variables, parameters, tuple fields, `param`s | snake_case | `request_count` |
| Test cases | snake_case | `lookup_all_languages` |

Hungarian prefixes, as in P's own examples, are allowed in any of them: a
prefix followed by a TitleCase name, like `tLookup`, `eResponse` or
`tcSingleClient`, is fine.

To change the style, put a `.plint.toml` in your project. The nearest one in
a file's directory or above it applies; settings it leaves out keep their
defaults:

```toml
[naming]
types = "TitleCase"
enum_constants = "SCREAMING_CASE"
events = "TitleCase"
machines = "TitleCase"   # machines, specs, scenarios and interfaces
states = "TitleCase"
modules = "TitleCase"
functions = "snake_case"
variables = "snake_case"
test_cases = "snake_case"
prefixes = ["t", "e", "tc"]   # [] to allow none
```

Each case is one of `TitleCase`, `camelCase`, `snake_case`, `SCREAMING_CASE`,
or `off` to not check that kind of name.

## Lint checks

Besides naming, p-lsp warns about:

| Check | Setting | What it finds |
|---|---|---|
| Unused declarations | `unused` | Types, events, event sets, functions, `param`s, machine variables and local variables nothing refers to. A library's declarations count as used if a project including it uses them. |
| Events never sent | `never_sent` | Events that are handled, deferred, ignored or observed, but never sent, raised or announced, and the specs observing them. |
| Unreachable states | `unreachable_states` | States that no chain of `goto`s leads to from the start state. |
| Unreachable code | `unreachable_code` | Statements after a `return`, `raise`, `goto`, `break` or `continue`, or after an `if` whose branches all leave. |
| Shadowing | `shadowing` | Parameters and local variables that hide a machine variable. |
| Order | `order` | A start state that isn't its machine's first state, and a state's `entry` and `exit` out of order: `entry` first, then `exit`, then its other handlers, `defer`s and `ignore`s. |

Most come with a quick fix: an unused declaration can be removed (just its
name, from a list like `var a, b: int;`), as can unreachable code and an
unreachable state nothing refers to, and an out-of-order start state,
`entry` or `exit` can be moved into place. Events never sent and shadowing have no
fix, since only you know what was meant.

Uses are counted by name, so a check can miss an unused declaration whose
name is also used for something else, but it won't report one that's
used. Each check's level is `warning` (the default), `error` or `off`, set
in the same `.plint.toml`:

```toml
[checks]
unused = "warning"
never_sent = "warning"
unreachable_states = "error"
unreachable_code = "warning"
shadowing = "off"
order = "warning"
```

To check from the command line, as in CI, run `p-lsp lint`. It checks the P
files under each path given (the current directory by default) against the
files compiled with them, prints each problem as
`file:line:column: message (check)`, and exits with 1 if it found any.
`-json` prints them as LSP diagnostics instead, each with its `file`:

```sh
p-lsp lint ./...
```

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
code --install-extension p-lsp-0.5.1.vsix
```

It highlights with the server's semantic tokens. Set `p-lsp.path` if
`p-lsp` isn't on the `PATH` VS Code sees, and `p-lsp.args` to pass flags
such as `["-compiler", ""]`. Format Document formats with pfmt; to format
on save, add `"[p]": { "editor.formatOnSave": true }` to `settings.json`.

Lint warnings show in the files you have open, as in any editor. To see
them for every P file in the workspace, run **P: Lint Workspace** from the
Command Palette: it runs `p-lsp lint` and puts what it finds in the
Problems panel.

Above each test case, **Run test** compiles the project and checks the test
case with `p check`, in a terminal; if PChecker finds a bug, it offers to
step through the bug's trace in the debugger. **Debug test** has prdb check
the test case until PChecker finds a bug, and debugs that. `p.command` sets
the command that runs the P tool, if it isn't `p` on the `PATH` or in
`~/.dotnet/tools`, and `p.schedules` how many schedules PChecker explores
(1000 by default). The lenses come from the server, as `p.runTest` and
`p.debugTest` commands for the editor to run, so other editors show them
but need their own handlers to run them.

The extension also runs [prdb](https://github.com/bencornia/prdb), a replay
debugger for PChecker's traces: with prdb on the `PATH` (or `prdb.path`
set), start debugging in a `.p` file and pick a trace to step through.
While debugging, the toolbar's Show Trace Diagram button opens the trace's
diagram beside the editor, following the current step; click a step to go
there.

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
- [x] Naming style warnings, configurable with `.plint.toml`, with a quick fix that renames the declaration everywhere, and `p-lsp lint` for the command line
- [x] Lint checks: unused declarations, events never sent, unreachable states and code, and shadowed machine variables
- [x] Run test and Debug test lenses over each test case, run in VS Code with `p check` and prdb

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
