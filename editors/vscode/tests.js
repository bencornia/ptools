// Runs and debugs the test cases plsp puts lenses over. Run test runs
// prdb check in a terminal, which compiles the project and checks the test
// case with PChecker; if it finds a bug, it offers to step through the
// bug's trace with prdb. Debug test has prdb check the test case itself,
// until PChecker finds a bug, and debug that.
const cp = require('child_process');
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
    const run = new TestRun(test.name, project, vscode.workspace.getConfiguration('prdb').get('path'), config.get('command'), schedules);
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

// TestRun is the terminal a test case runs in: prdb check, showing the P
// tool's output as it goes. done resolves when it's finished, to
// { bug: { test, trace } } for the bug PChecker found, if any, or { error }
// if the test couldn't be run.
class TestRun {
    constructor(name, project, prdb, compiler, schedules) {
        Object.assign(this, { name, project, prdb, compiler, schedules });
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

    // run runs prdb check, and resolves to what it found.
    run() {
        const args = ['check', '--json', '--project', this.project, '--schedules', String(this.schedules)];
        for (const arg of this.compiler?.length ? this.compiler : []) {
            args.push('--compiler', arg);
        }
        args.push(this.name);
        this.write(`> ${[path.basename(this.prdb), ...args].join(' ')}\n`);
        return new Promise((resolve) => {
            let stdout = '';
            let stderr = '';
            const child = cp.spawn(this.prdb, args, { cwd: this.project });
            this.child = child;
            child.stdout.on('data', (data) => (stdout += data));
            child.stderr.on('data', (data) => {
                stderr += data;
                this.write(String(data));
            });
            child.on('error', (err) =>
                resolve({ error: err.code === 'ENOENT' ? `can't run ${this.prdb}; set prdb.path to the prdb binary` : err.message }),
            );
            child.on('close', (code) => resolve(checkResult(code, stdout, stderr)));
        });
    }

    write(text) {
        this.writer.fire(text.replace(/\r?\n/g, '\r\n'));
    }
}

// checkResult reads what prdb check --json found from its exit status and
// output: { bug } for a bug, {} for none, or { error } with the last line
// it wrote to standard error, which says what went wrong.
function checkResult(code, stdout, stderr) {
    let result;
    try {
        result = JSON.parse(stdout);
    } catch {
        const lines = stderr.trim().split('\n');
        return { error: lines[lines.length - 1].trim() || `prdb check exited with ${code}` };
    }
    return result.bug ? { bug: result.bug } : {};
}

// schedulesSetting returns p.schedules, or its default, 1000, if it's unset
// or not a positive whole number. VS Code can run an extension's commands
// before it knows the defaults of the settings it contributes.
function schedulesSetting(config) {
    const schedules = config.get('schedules');
    return Number.isInteger(schedules) && schedules > 0 ? schedules : 1000;
}

module.exports = { registerTests, checkResult, schedulesSetting, TestRun };
