// Starts p-lsp for P files, and runs prdb to replay PChecker traces.
const path = require('path');
const vscode = require('vscode');
const { LanguageClient } = require('vscode-languageclient/node');

let client;

async function activate(context) {
    context.subscriptions.push(
        vscode.debug.registerDebugAdapterDescriptorFactory('prdb', { createDebugAdapterDescriptor }),
        vscode.debug.registerDebugConfigurationProvider('prdb', { resolveDebugConfiguration }),
        vscode.commands.registerCommand('prdb.pickTrace', pickTrace),
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

// With no launch.json, debugging a P file replays a trace picked from the
// workspace.
function resolveDebugConfiguration(folder, config) {
    if (!config.type && !config.request && !config.name) {
        return { type: 'prdb', request: 'launch', name: 'Replay a P trace', trace: '${command:pickTrace}' };
    }
    return config;
}

// Asks which of the workspace's PChecker traces or schedules to replay,
// newest first. Returning undefined cancels the launch.
async function pickTrace() {
    const uris = await vscode.workspace.findFiles('**/*.{trace.json,schedule}', '**/node_modules/**');
    if (uris.length === 0) {
        vscode.window.showErrorMessage(
            'No PChecker traces (*.trace.json) or schedules (*.schedule) in the workspace. Run p check to find a bug, or p check -v to trace a run.',
        );
        return undefined;
    }
    const traces = await Promise.all(
        uris.map(async (uri) => ({ uri, mtime: (await vscode.workspace.fs.stat(uri)).mtime })),
    );
    traces.sort((a, b) => b.mtime - a.mtime);
    const items = traces.map(({ uri, mtime }) => ({
        label: path.basename(uri.fsPath),
        description: traceDirectory(uri),
        detail: new Date(mtime).toLocaleString(),
        uri,
    }));
    const picked = await vscode.window.showQuickPick(items, { placeHolder: 'Trace to replay' });
    return picked?.uri.fsPath;
}

// traceDirectory names the directory a trace is in, relative to the
// workspace, and empty at its top.
function traceDirectory(uri) {
    const dir = path.dirname(vscode.workspace.asRelativePath(uri));
    return dir === '.' ? '' : dir;
}

module.exports = { activate, deactivate, createDebugAdapterDescriptor, resolveDebugConfiguration, pickTrace };
