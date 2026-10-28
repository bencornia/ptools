// Starts p-lsp for P files, and runs prdb to replay PChecker traces, or to
// debug the bug PChecker finds in a test case.
const path = require('path');
const vscode = require('vscode');
const { LanguageClient } = require('vscode-languageclient/node');
const { registerDiagram } = require('./diagram');

let client;

async function activate(context) {
    context.subscriptions.push(
        vscode.debug.registerDebugAdapterDescriptorFactory('prdb', { createDebugAdapterDescriptor }),
        vscode.debug.registerDebugConfigurationProvider('prdb', {
            resolveDebugConfiguration,
            resolveDebugConfigurationWithSubstitutedVariables,
        }),
        vscode.commands.registerCommand('prdb.pickTrace', pickTrace),
        vscode.commands.registerCommand('prdb.pickTestCase', pickTestCase),
    );
    registerDiagram(context);

    const config = vscode.workspace.getConfiguration('p-lsp');
    client = new LanguageClient(
        'p-lsp',
        'P',
        { command: config.get('path'), args: config.get('args') },
        { documentSelector: [{ scheme: 'file', language: 'p' }] },
    );
    await client.start();
}

function deactivate() {
    return client?.stop();
}

// Runs prdb in the session's workspace folder, so relative trace paths in
// launch.json work.
function createDebugAdapterDescriptor(session) {
    const config = vscode.workspace.getConfiguration('prdb');
    return new vscode.DebugAdapterExecutable(config.get('path'), config.get('args'), {
        cwd: session.workspaceFolder?.uri.fsPath,
    });
}

// With no launch.json, debugging a P file asks what to debug: one of the
// workspace's traces or schedules, or a test case to check for a bug.
// Returning undefined cancels the launch.
async function resolveDebugConfiguration(folder, config) {
    if (config.type || config.request || config.name) {
        return config;
    }
    const traces = await traceItems();
    const tests = await testCaseItems();
    if (traces.length === 0 && tests.length === 0) {
        vscode.window.showErrorMessage('No PChecker traces, schedules or test cases in the workspace.');
        return undefined;
    }
    const items = [...traces];
    if (tests.length > 0) {
        items.push({ label: 'Check until PChecker finds a bug', kind: vscode.QuickPickItemKind.Separator }, ...tests);
    }
    const picked = await vscode.window.showQuickPick(items, { placeHolder: 'Trace, schedule or test case to debug' });
    if (!picked) {
        return undefined;
    }
    if (picked.test) {
        return {
            type: 'prdb',
            request: 'launch',
            name: `Find a bug in ${picked.test.name}`,
            testcase: picked.test.name,
            project: picked.test.project,
        };
    }
    return { type: 'prdb', request: 'launch', name: 'Replay a P trace', trace: picked.uri.fsPath };
}

// Gives a test case the project it's declared in, unless the configuration
// names one, and asks which schedule of a --verbose trace with several to
// replay, unless it names one.
async function resolveDebugConfigurationWithSubstitutedVariables(folder, config) {
    if (config.testcase && !config.project) {
        const test = (await testCases()).find((t) => t.name === config.testcase);
        if (test) {
            config.project = test.project;
        }
    }
    if (config.trace?.endsWith('.trace.json') && config.schedule === undefined) {
        const file = path.resolve(folder?.uri.fsPath ?? '', config.trace);
        const schedule = await pickSchedule(vscode.Uri.file(file));
        if (schedule === null) {
            return undefined;
        }
        if (schedule !== undefined) {
            config.schedule = schedule;
        }
    }
    return config;
}

// pickSchedule asks which of a --verbose trace's schedules to replay, last
// first, as that's the one that found the bug if one did. It returns the
// schedule's number, counting from 1, undefined if the trace has only one,
// or null if none was picked.
async function pickSchedule(uri) {
    let schedules;
    try {
        schedules = JSON.parse(new TextDecoder().decode(await vscode.workspace.fs.readFile(uri)));
    } catch {
        return undefined; // prdb reports what's wrong with it
    }
    if (!Array.isArray(schedules) || !Array.isArray(schedules[0]) || schedules.length < 2) {
        return undefined;
    }
    const items = schedules
        .map((steps, i) => {
            const program = steps.filter((step) => step.type !== 'StrategyDescription');
            return {
                label: `Schedule ${i + 1}`,
                description: `${program.length} steps`,
                detail: program.at(-1)?.details?.log,
                schedule: i + 1,
            };
        })
        .reverse();
    const picked = await vscode.window.showQuickPick(items, {
        placeHolder: `${path.basename(uri.fsPath)} has ${schedules.length} schedules: which to replay?`,
    });
    return picked ? picked.schedule : null;
}

// Asks which of the workspace's PChecker traces or schedules to replay,
// newest first. Returning undefined cancels the launch.
async function pickTrace() {
    const items = await traceItems();
    if (items.length === 0) {
        vscode.window.showErrorMessage(
            'No PChecker traces (*.trace.json) or schedules (*.schedule) in the workspace. Run p check to find a bug, or p check -v to trace a run.',
        );
        return undefined;
    }
    const picked = await vscode.window.showQuickPick(items, { placeHolder: 'Trace to replay' });
    return picked?.uri.fsPath;
}

// Asks which of the workspace's test cases to check for a bug.
async function pickTestCase() {
    const items = await testCaseItems();
    if (items.length === 0) {
        vscode.window.showErrorMessage('No test cases in the workspace\'s P files.');
        return undefined;
    }
    const picked = await vscode.window.showQuickPick(items, { placeHolder: 'Test case to check for a bug' });
    return picked?.test.name;
}

// traceItems lists the workspace's traces and schedules, newest first.
async function traceItems() {
    const uris = await vscode.workspace.findFiles('**/*.{trace.json,schedule}', '**/node_modules/**');
    const traces = await Promise.all(
        uris.map(async (uri) => ({ uri, mtime: (await vscode.workspace.fs.stat(uri)).mtime })),
    );
    traces.sort((a, b) => b.mtime - a.mtime);
    return traces.map(({ uri, mtime }) => ({
        label: path.basename(uri.fsPath),
        description: traceDirectory(uri),
        detail: new Date(mtime).toLocaleString(),
        uri,
    }));
}

async function testCaseItems() {
    return (await testCases()).map((test) => ({
        label: test.name,
        description: [test.detail, vscode.workspace.asRelativePath(test.uri)].filter(Boolean).join(' · '),
        test,
    }));
}

// testCases finds the test cases declared in the workspace's P files, by
// name, with the project each is in: the nearest directory, from its
// file's up, with a .pproj file. A parameterised test is listed by its own
// name, which checks every case, and, if no assume or twise drops some,
// by each case's.
async function testCases() {
    const uris = await vscode.workspace.findFiles('**/*.p', '**/node_modules/**');
    const tests = [];
    for (const uri of uris) {
        const text = new TextDecoder().decode(await vscode.workspace.fs.readFile(uri));
        const project = await projectOf(uri);
        for (const decl of testDeclarations(text)) {
            if (!decl.params) {
                tests.push({ name: decl.name, uri, project });
                continue;
            }
            tests.push({ name: decl.name, uri, project, detail: 'every case' });
            if (!decl.filtered) {
                for (const values of combinations(decl.params)) {
                    tests.push({
                        name: decl.name + '___' + values.map(([p, v]) => `${p}_${v}`).join('__'),
                        uri,
                        project,
                        detail: values.map(([p, v]) => `${p} = ${v}`).join(', '),
                    });
                }
            }
        }
    }
    return tests.sort((a, b) => a.name.localeCompare(b.name));
}

// testDeclarations reads the test declarations in P source, outside
// comments: test [param (p in [v, ...], ...)] [assume (...)] [twise] name [...
function testDeclarations(text) {
    text = text.replace(/\/\*[\s\S]*?\*\/|\/\/.*$/gm, (c) => c.replace(/[^\n]/g, ' '));
    const decls = [];
    for (const match of text.matchAll(/^\s*test\b/gm)) {
        const end = text.indexOf('[main', match.index);
        const header = text.slice(match.index + match[0].length, end < 0 ? undefined : end);
        const name = header.match(/(\w+)\s*$/)?.[1];
        if (!name || end < 0) continue;
        const decl = { name, filtered: /\bassume\b|\bwise\b|\bpairwise\b/.test(header) };
        const param = header.match(/^\s*param\s*\(/);
        if (param) {
            decl.params = [];
            for (const p of header.matchAll(/(\w+)\s+in\s+\[([^\]]*)\]/g)) {
                decl.params.push([p[1], p[2].split(',').map((v) => v.trim()).filter(Boolean)]);
            }
        }
        decls.push(decl);
    }
    return decls;
}

// combinations lists each choice of a value for every parameter, the
// first parameter's changing fastest, as P orders a test's cases.
function combinations(params) {
    let out = [[]];
    for (const [name, values] of params) {
        const next = [];
        for (const v of values) for (const prefix of out) next.push([...prefix, [name, v]]);
        out = next;
    }
    return out;
}

async function projectOf(uri) {
    for (let dir = path.dirname(uri.fsPath); ; dir = path.dirname(dir)) {
        try {
            const entries = await vscode.workspace.fs.readDirectory(vscode.Uri.file(dir));
            if (entries.some(([name]) => name.endsWith('.pproj'))) {
                return dir;
            }
        } catch {
            // Unreadable; keep looking up.
        }
        if (path.dirname(dir) === dir) {
            return undefined;
        }
    }
}

// traceDirectory names the directory a trace is in, relative to the
// workspace, and empty at its top.
function traceDirectory(uri) {
    const dir = path.dirname(vscode.workspace.asRelativePath(uri));
    return dir === '.' ? '' : dir;
}

module.exports = {
    activate,
    deactivate,
    createDebugAdapterDescriptor,
    resolveDebugConfiguration,
    resolveDebugConfigurationWithSubstitutedVariables,
    pickTrace,
    pickTestCase,
};
