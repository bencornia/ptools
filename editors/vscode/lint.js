// Lints every P file in the workspace with plsp lint, for the Problems
// panel. plsp checks the files open in the editor itself, so
// only the rest are shown from here: when a file opens, the server takes
// over its warnings, and when it closes, they come back here.
const cp = require('child_process');
const vscode = require('vscode');

let collection;
// checked is whether the workspace has been checked, so files closed since
// get their warnings back.
let checked = false;

function registerLint(context) {
    collection = vscode.languages.createDiagnosticCollection('plsp lint');
    context.subscriptions.push(
        collection,
        vscode.commands.registerCommand('plsp.lintWorkspace', lintWorkspace),
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
        vscode.window.showErrorMessage('Open a folder to lint its P files.');
        return;
    }
    let problems;
    try {
        problems = await vscode.window.withProgress(
            { location: vscode.ProgressLocation.Window, title: 'Linting P files' },
            () => lint(folders),
        );
    } catch (e) {
        vscode.window.showErrorMessage(`plsp lint failed: ${e.message}`);
        return;
    }
    checked = true;
    collection.clear();
    show(problems);
    const count = [...problems.values()].reduce((n, list) => n + list.length, 0);
    if (count === 0) {
        vscode.window.showInformationMessage('No lint problems in the workspace\'s P files.');
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

// lint runs plsp lint on paths, and returns the diagnostics of each file
// it checked, by path; files with no problems have none.
function lint(paths) {
    const config = vscode.workspace.getConfiguration('plsp');
    return new Promise((resolve, reject) => {
        cp.execFile(config.get('path'), ['lint', '--json', ...paths], { maxBuffer: 64 << 20 }, (err, stdout, stderr) => {
            // 1 means it found problems.
            if (err && err.code !== 1) {
                reject(new Error(err.code === 'ENOENT' ? `can't run ${config.get('path')}` : stderr.trim() || err.message));
                return;
            }
            let found;
            try {
                found = JSON.parse(stdout);
            } catch (e) {
                reject(new Error(`unexpected output from ${config.get('path')} lint; is it up to date?`));
                return;
            }
            const problems = new Map(paths.filter((p) => p.endsWith('.p')).map((p) => [p, []]));
            for (const p of found) {
                if (!problems.has(p.file)) problems.set(p.file, []);
                problems.get(p.file).push(toDiagnostic(p));
            }
            resolve(problems);
        });
    });
}

// toDiagnostic converts a problem plsp lint --json printed, an LSP
// diagnostic with its file, to a VS Code one.
function toDiagnostic(p) {
    const position = (pos) => new vscode.Position(pos.line, pos.character);
    const range = new vscode.Range(position(p.range.start), position(p.range.end));
    const severity = p.severity === 1 ? vscode.DiagnosticSeverity.Error : vscode.DiagnosticSeverity.Warning;
    const diagnostic = new vscode.Diagnostic(range, p.message, severity);
    diagnostic.source = p.source;
    diagnostic.code = p.code;
    if ((p.tags ?? []).includes(1)) {
        diagnostic.tags = [vscode.DiagnosticTag.Unnecessary];
    }
    return diagnostic;
}

module.exports = { registerLint, lintWorkspace, toDiagnostic };
