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
location, without an editor.

## Helix

Add a debugger to the `p` language in `~/.config/helix/languages.toml`, after
its other settings:

```toml
[language.debugger]
name = "prdb"
transport = "stdio"
command = "prdb"

[[language.debugger.templates]]
name = "replay a trace"
request = "launch"
completion = [ { name = "trace (.trace.json)", completion = "filename" } ]
args = { trace = "{0}" }
```

Then `space G l` launches it and asks for the trace. `space G b` toggles a
breakpoint, `c` continues, `n` steps to the machine's next step, `i` to the
next step of any machine, `o` out, `v` lists variables, and `s` switches
machines. Helix's debugger support is experimental, and has no step back.

## Neovim

With [nvim-dap](https://github.com/mfussenegger/nvim-dap):

```lua
local dap = require('dap')
dap.adapters.prdb = { type = 'executable', command = 'prdb' }
dap.configurations.p = {
  {
    type = 'prdb',
    request = 'launch',
    name = 'Replay a trace',
    trace = function() return vim.fn.input('Trace: ', '', 'file') end,
  },
}
```

## Launch arguments

| Argument | |
|---|---|
| `trace` | The `.trace.json` file to replay. Required. |
| `project` | The P project's directory, if it can't be found from the trace's. |
| `stopOnEntry` | Stop at the first step; true unless set false, which runs to the first breakpoint. |

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
- [x] Helix setup
- [ ] Launch from a `.schedule` file, replaying it with `p check --replay -v`
- [ ] Launch a test case: run `p check` to find a bug, then replay it
- [ ] Choose which schedule of a `--verbose` trace with several to replay
- [ ] VS Code: add the debugger to p-lsp's extension
- [ ] Test the Neovim setup
- [ ] A sequence diagram of a trace, from its vector clocks
