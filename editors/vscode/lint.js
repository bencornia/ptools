// Checks the naming of every P file in the workspace with p-lsp lint, for
// the Problems panel. p-lsp checks the files open in the editor itself, so
// only the rest are shown from here: when a file opens, the server takes
// over its warnings, and when it closes, they come back here.
const cp = require('child_process');
const vscode = require('vscode');

let collection;
// checked is whether the workspace has been checked, so files closed since
// get their warnings back.
let checked = false;

function registerLint(context) {
    collection = vscode.languages.createDiagnosticCollection('p-lsp lint');
    context.subscriptions.push(
        collection,
        vscode.commands.registerCommand('p-lsp.lintWorkspace', lintWorkspace),
        vscode.workspace.onDidOpenTextDocument((doc) => collection.delete(doc.uri)),
        vscode.workspace.onDidCloseTextDocument((doc) => {
            if (checked && doc.uri.scheme === 'file' && doc.languageId === 'p') {
                lint([doc.uri.fsPath]).then(show, () => {});
            }
        }),
    );
}

// lintWorkspace checks every workspace folder and shows what it finds.
async function lintWorkspace() {
    const folders = (vscode.workspace.workspaceFolders ?? []).map((f) => f.uri.fsPath);
    if (folders.length === 0) {
        vscode.window.showErrorMessage('Open a folder to check the naming of its P files.');
        return;
    }
    let problems;
    try {
        problems = await vscode.window.withProgress(
            { location: vscode.ProgressLocation.Window, title: 'Checking P naming' },
            () => lint(folders),
        );
    } catch (e) {
        vscode.window.showErrorMessage(`p-lsp lint failed: ${e.message}`);
        return;
    }
    checked = true;
    collection.clear();
    show(problems);
    const count = [...problems.values()].reduce((n, list) => n + list.length, 0);
    if (count === 0) {
        vscode.window.showInformationMessage('No naming problems in the workspace\'s P files.');
        return;
    }
    vscode.commands.executeCommand('workbench.actions.view.problems');
}

// show sets the warnings of the files that aren't open, and clears those
// of files checked and found fine.
function show(problems) {
    const open = new Set(vscode.workspace.textDocuments.map((doc) => doc.uri.fsPath));
    for (const [file, diagnostics] of problems) {
        if (!open.has(file)) {
            collection.set(vscode.Uri.file(file), diagnostics);
        }
    }
}

// lint runs p-lsp lint on paths, and returns the diagnostics of each file
// it checked, by path; files with no problems have none.
function lint(paths) {
    const config = vscode.workspace.getConfiguration('p-lsp');
    return new Promise((resolve, reject) => {
        cp.execFile(config.get('path'), ['lint', ...paths], { maxBuffer: 64 << 20 }, (err, stdout, stderr) => {
            // 1 means it found problems.
            if (err && err.code !== 1) {
                reject(new Error(err.code === 'ENOENT' ? `can't run ${config.get('path')}` : stderr.trim() || err.message));
                return;
            }
            const problems = new Map(paths.filter((p) => p.endsWith('.p')).map((p) => [p, []]));
            for (const line of stdout.split('\n')) {
                const d = parseLine(line);
                if (d) {
                    if (!problems.has(d.file)) problems.set(d.file, []);
                    problems.get(d.file).push(d.diagnostic);
                }
            }
            resolve(problems);
        });
    });
}

// parseLine reads a line of p-lsp lint's output, file:line:column: message,
// as a warning underlining the name the message is about.
function parseLine(line) {
    const m = line.match(/^(.*):(\d+):(\d+): (.*)$/);
    if (!m) {
        return undefined;
    }
    const [, file, row, col, message] = m;
    const start = new vscode.Position(Number(row) - 1, Number(col) - 1);
    const name = message.match(/`([^`]+)`/)?.[1] ?? '';
    const range = new vscode.Range(start, start.translate(0, name.length));
    const diagnostic = new vscode.Diagnostic(range, message, vscode.DiagnosticSeverity.Warning);
    diagnostic.source = 'p-lsp';
    diagnostic.code = 'naming';
    return { file, diagnostic };
}

module.exports = { registerLint, lintWorkspace, parseLine };
