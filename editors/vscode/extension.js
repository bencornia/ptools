// Starts p-lsp for P files.
const vscode = require('vscode');
const { LanguageClient } = require('vscode-languageclient/node');

let client;

async function activate() {
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

module.exports = { activate, deactivate };
