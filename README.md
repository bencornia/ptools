# prdb

A replay debugger for the [P programming language](https://github.com/p-org/P).

When PChecker finds a bug, it writes a trace of the schedule that found it:
which machine did what, in what order. prdb steps through that trace in your
editor, over the Debug Adapter Protocol, showing each step against your P
source:

- Each machine and spec monitor is a thread.
- Each step shows where it happened: the `entry`, `exit` or `on` handler
  that ran, or the `send`, `raise`, `announce`, `goto`, `print` or `receive`
  it did, including inside helper functions and projects included with
  `<IncludeProject>`.
- The variables show the step's event, its payload (expandable), its sender
  and target, and the machine's state and the events waiting in its inbox.
- Replay stops at the failed assertion, on its line, with its message.

You can step forward and back through the trace:

| Command | Moves to |
|---|---|
| Step in | the next step of any machine |
| Next | the selected machine's next step |
| Step out | the start of the selected machine's next handler |
| Continue | the next breakpoint, or the failed assertion |
| Step back | the selected machine's previous step |
| Reverse continue | the previous breakpoint, or the first step |

A breakpoint stops at steps on its line, or, on a line inside a handler
where no step happens, at the start of that handler.

## Getting a trace

```sh
p compile
p check -tc tcMyTest -s 1000
```

When PChecker finds a bug, it writes
`PCheckerOutput/BugFinding/<Project>_0_0.trace.json`; that's the file to
debug. To step through a run that didn't fail, run `p check` with
`--verbose` (`-v`), which writes `<Project>_0_verbose.trace.json` either way.

Or debug the `.schedule` file PChecker writes beside the trace. prdb
compiles the project and replays the schedule with `p check --replay`, which
is deterministic, to make the trace itself. This needs the P compiler: `p`
on the `PATH`, or, for an editor started without your shell's `PATH`, where
`dotnet tool install --global P` puts it, `~/.dotnet/tools/p`. It replays
against the source as it is now, so it's the way to see whether a fix
changes what happens.

prdb finds the project's source from the trace's location: the nearest
directory, from the trace's up, with a `.pproj` file.

## What it can't show

The trace records events, states, payloads and prints, not machines'
variables or individual statements, so prdb can't show a machine's
variables, or step between statements that don't send, raise, announce,
goto, print or receive.

## Installing

```sh
go build -o ~/.local/bin/prdb .
```

Building requires cgo (a C compiler), since tree-sitter is a C library.
While the grammar's repository is private, Go has to fetch it over SSH:

```sh
go env -w 'GOPRIVATE=github.com/bencornia/*'
git config --global url."git@github.com:".insteadOf "https://github.com/"
```

`prdb -list trace.json` prints each step of a trace with its source
location, without an editor. It takes a `.schedule` file too.

## VS Code

The debugger is part of the P extension, in p-lsp's
[`editors/vscode`](https://github.com/bencornia/p-lsp/tree/main/editors/vscode).
Install prdb, then build and install the extension:

```sh
cd p-lsp/editors/vscode
npm install
npx @vscode/vsce package --skip-license
code --install-extension p-lsp-0.2.0.vsix
```

Open a `.p` file and start debugging (F5, or Run and Debug): with no
`launch.json`, it asks which of the workspace's traces and schedules to
replay, newest first. To keep a configuration, add one with the "P: Replay a trace" snippet:

```json
{
  "type": "prdb",
  "request": "launch",
  "name": "Replay a P trace",
  "trace": "${command:pickTrace}"
}
```

Set `trace` to a path, relative to the workspace folder, to skip the picker.
Breakpoints go in the gutter of `.p` files; the toolbar's Step Back and
Reverse buttons move backward. Set `prdb.path` if `prdb` isn't on the `PATH`
VS Code sees, and `prdb.args` to `["-v"]` to log the protocol.

## Other editors

With [nvim-dap](https://github.com/mfussenegger/nvim-dap) in Neovim:

```lua
local dap = require('dap')
dap.adapters.prdb = { type = 'executable', command = 'prdb' }
dap.configurations.p = {
  {
    type = 'prdb',
    request = 'launch',
    name = 'Replay a P trace',
    trace = function() return vim.fn.input('Trace: ', '', 'file') end,
  },
}
```

Helix's debugger support is experimental, and has no step back. prdb works
around its not selecting the thread at the first stop, so with a
`[language.debugger]` for `p` whose launch template passes `trace`, it can
step forward through a trace.

## Launch arguments

| Argument | |
|---|---|
| `trace` | The `.trace.json` file to replay, or a `.schedule` file to compile and replay. Required. |
| `project` | The P project's directory, if it can't be found from the trace's. |
| `stopOnEntry` | Stop at the first step; true unless set false, which runs to the first breakpoint. |
| `compiler` | The command that runs the P tool, to replay a schedule: `["p"]` by default. |

## Developing

```sh
go test ./...
```

`testdata/pingpong` is a small P project with a planted bug, and the trace
PChecker wrote for it.

## TODO

- [x] Read PChecker's traces, of a bug or with `--verbose`
- [x] Map each step to its handler or statement, through helper functions and included projects
- [x] Debug adapter: a thread per machine, stepping both ways, breakpoints, payloads and inboxes, stopping at the failed assertion
- [x] `prdb -list`
- [x] VS Code: the debugger in p-lsp's VS Code extension, with a trace picker
- [x] Launch from a `.schedule` file, replaying it with `p check --replay -v`
- [ ] Launch a test case: run `p check` to find a bug, then replay it
- [ ] Choose which schedule of a `--verbose` trace with several to replay
- [x] Try the VS Code debugger in VS Code itself
- [ ] Test the Neovim setup
- [ ] A sequence diagram of a trace, from its vector clocks
