// Runs and debugs the test cases plsp puts lenses over. Run test compiles
// the project and checks the test case with p check in a terminal; if
// PChecker finds a bug, it offers to step through the bug's trace with
// prdb. Debug test has prdb check the test case itself, until PChecker
// finds a bug, and debug that.
const cp = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');
const vscode = require('vscode');

function registerTests(context, projectOf) {
    context.subscriptions.push(
        vscode.commands.registerCommand('p.runTest', (test) => runTest(test, projectOf)),
        vscode.commands.registerCommand('p.debugTest', (test) => debugTest(test, projectOf)),
    );
}

// runTest runs the test case test, a lens's argument: { name, file,
// project }, in a terminal of its own.
async function runTest(test, projectOf) {
    const project = test.project || (await projectOf(vscode.Uri.file(test.file)));
    if (!project) {
        vscode.window.showErrorMessage(`No .pproj file in or above ${path.dirname(test.file)} to compile ${test.name} with.`);
        return;
    }
    const config = vscode.workspace.getConfiguration('p');
    const schedules = schedulesSetting(config);
    const run = new TestRun(test.name, project, pCommand(config.get('command')), schedules);
    const task = new vscode.Task(
        { type: 'p', test: test.name },
        vscode.workspace.getWorkspaceFolder(vscode.Uri.file(test.file)) ?? vscode.TaskScope.Workspace,
        `Run ${test.name}`,
        'P',
        new vscode.CustomExecution(async () => run),
    );
    task.presentationOptions = { reveal: vscode.TaskRevealKind.Always, clear: true };
    await vscode.tasks.executeTask(task);
    const result = await run.done;
    if (result.error) {
        vscode.window.showErrorMessage(`Running ${test.name}: ${result.error}`);
    } else if (result.bug) {
        const picked = await vscode.window.showErrorMessage(`PChecker found a bug in ${result.bug.test}.`, 'Debug');
        if (picked === 'Debug') {
            await vscode.debug.startDebugging(undefined, {
                type: 'prdb',
                request: 'launch',
                name: `Debug ${result.bug.test}`,
                trace: result.bug.trace,
                project,
            });
        }
    } else {
        vscode.window.showInformationMessage(`PChecker found no bug in ${test.name} in ${schedules} schedules.`);
    }
}

// debugTest has prdb check the test case until PChecker finds a bug, and
// debug it.
async function debugTest(test, projectOf) {
    const project = test.project || (await projectOf(vscode.Uri.file(test.file)));
    const config = vscode.workspace.getConfiguration('p');
    await vscode.debug.startDebugging(vscode.workspace.getWorkspaceFolder(vscode.Uri.file(test.file)), {
        type: 'prdb',
        request: 'launch',
        name: `Find a bug in ${test.name}`,
        testcase: test.name,
        project,
        schedules: schedulesSetting(config),
        compiler: config.get('command')?.length ? config.get('command') : ['p'],
    });
}

// TestRun is the terminal a test case runs in: it compiles the project,
// then checks the test case, showing their output as they go. done
// resolves when it's finished, to { bug: { test, trace } } for the bug
// PChecker found, if any, or { error } if the test couldn't be run.
class TestRun {
    constructor(name, project, command, schedules) {
        Object.assign(this, { name, project, command, schedules });
        this.writer = new vscode.EventEmitter();
        this.closer = new vscode.EventEmitter();
        this.onDidWrite = this.writer.event;
        this.onDidClose = this.closer.event;
        this.done = new Promise((resolve) => (this.finish = resolve));
    }

    async open() {
        const result = await this.run();
        this.finish(result);
        this.closer.fire(result.error || result.bug ? 1 : 0);
    }

    close() {
        this.child?.kill();
        this.finish({ error: 'stopped' });
    }

    async run() {
        const compiled = await this.spawn(['compile']);
        if (compiled.error) return compiled;
        if (compiled.code !== 0) return { error: `compiling ${path.basename(this.project)} failed` };
        const checked = await this.spawn(['check', '--testcase', this.name, '--schedules', String(this.schedules)]);
        if (checked.error) return checked;
        const { ran, bug } = findBug(checked.output, this.name, this.project);
        if (bug) return { bug };
        if (checked.code !== 0) return { error: `p check exited with ${checked.code}` };
        if (!ran) return { error: `p check found no test case ${this.name}; is the project compiled with its file?` };
        return {};
    }

    // spawn runs the P tool with args in the project, showing its output,
    // and resolves to its exit code and output, or { error }.
    spawn(args) {
        const [command, ...rest] = this.command.argv;
        this.write(`> ${[path.basename(command), ...rest, ...args].join(' ')}\n`);
        return new Promise((resolve) => {
            let output = '';
            const child = cp.spawn(command, [...rest, ...args], { cwd: this.project, env: this.command.env });
            this.child = child;
            const show = (data) => {
                output += data;
                this.write(String(data));
            };
            child.stdout.on('data', show);
            child.stderr.on('data', show);
            child.on('error', (err) =>
                resolve({ error: err.code === 'ENOENT' ? `can't run ${command}; set p.command to the command that runs the P tool` : err.message }),
            );
            child.on('close', (code) => resolve({ code, output }));
        });
    }

    write(text) {
        this.writer.fire(text.replace(/\r?\n/g, '\r\n'));
    }
}

// findBug reads p check's output for whether it ran the test case name,
// or one of a parameterised test's cases, named name___..., and the bug it
// found in them, if any: the case and the trace it wrote, relative paths
// being in project. Each case writes the same files, so it's the last bug
// whose trace is still there. -tc runs every test case whose name starts
// with name, so others may have run too.
function findBug(output, name, project) {
    let current;
    let ran = false;
    let bug;
    for (const raw of output.split('\n')) {
        const line = raw.trim();
        const test = line.match(/^\.\. Test case :: (.*)$/);
        if (test) {
            current = test[1].trim();
            ran ||= isCase(current, name);
            continue;
        }
        const ours = current !== undefined && isCase(current, name);
        const written = line.match(/^\.\.\.\.\. Writing (.*\.trace\.json)$/);
        if (ours && written) {
            bug = { test: current, trace: path.resolve(project, written[1]) };
        }
    }
    return { ran, bug };
}

// isCase reports whether the test case p check ran, test, is name or one
// of its cases.
function isCase(test, name) {
    return test === name || test.startsWith(name + '___');
}

// schedulesSetting returns p.schedules, or its default, 1000, if it's unset
// or not a positive whole number. VS Code can run an extension's commands
// before it knows the defaults of the settings it contributes.
function schedulesSetting(config) {
    const schedules = config.get('schedules');
    return Number.isInteger(schedules) && schedules > 0 ? schedules : 1000;
}

// pCommand finds the command that runs the P tool, and the environment to
// run it in. Editors started from a desktop often lack the PATH and
// DOTNET_ROOT a shell profile sets, so when it's the default p and isn't
// on the PATH, it looks where `dotnet tool install --global` puts it, as
// prdb does.
function pCommand(argv) {
    argv = argv?.length ? argv : ['p'];
    const env = { ...process.env };
    if (argv[0] !== 'p' || onPath('p')) {
        return { argv, env };
    }
    const dotnet = path.join(os.homedir(), '.dotnet');
    const tool = path.join(dotnet, 'tools', 'p');
    if (!fs.existsSync(tool)) {
        return { argv, env };
    }
    const root = env.DOTNET_ROOT || dotnet;
    if (fs.existsSync(path.join(root, 'dotnet'))) {
        env.DOTNET_ROOT = root;
        env.PATH = root + path.delimiter + (env.PATH ?? '');
    }
    return { argv: [tool, ...argv.slice(1)], env };
}

function onPath(name) {
    return (process.env.PATH ?? '').split(path.delimiter).some((dir) => dir && fs.existsSync(path.join(dir, name)));
}

module.exports = { registerTests, findBug, pCommand, schedulesSetting, TestRun };
