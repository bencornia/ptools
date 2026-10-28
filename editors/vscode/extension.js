// Starts p-lsp for P files, and runs prdb to replay PChecker traces, or to
// debug the bug PChecker finds in a test case.
const path = require('path');
const vscode = require('vscode');
const { LanguageClient } = require('vscode-languageclient/node');

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
// names one.
async function resolveDebugConfigurationWithSubstitutedVariables(folder, config) {
    if (config.testcase && !config.project) {
        const test = (await testCases()).find((t) => t.name === config.testcase);
        if (test) {
            config.project = test.project;
        }
    }
    return config;
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
        description: vscode.workspace.asRelativePath(test.uri),
        test,
    }));
}

// testCases finds the test cases declared in the workspace's P files, by
// name, with the project each is in: the nearest directory, from its
// file's up, with a .pproj file.
async function testCases() {
    const uris = await vscode.workspace.findFiles('**/*.p', '**/node_modules/**');
    const tests = [];
    for (const uri of uris) {
        const text = new TextDecoder().decode(await vscode.workspace.fs.readFile(uri));
        for (const match of text.matchAll(/^\s*test\s+(\w+)/gm)) {
            tests.push({ name: match[1], uri, project: await projectOf(uri) });
        }
    }
    return tests.sort((a, b) => a.name.localeCompare(b.name));
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
