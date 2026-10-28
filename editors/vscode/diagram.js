// Shows a prdb session's trace as a diagram, in a panel beside the editor:
// the current step is highlighted as the debugger moves, and clicking a
// step takes the debugger there.
const crypto = require('crypto');
const vscode = require('vscode');

// panel is the diagram's panel while it's open, and shown the session it
// shows, until that ends.
let panel;
let shown;

function registerDiagram(context) {
    context.subscriptions.push(
        vscode.commands.registerCommand('prdb.showDiagram', () => showDiagram(vscode.debug.activeDebugSession)),
        vscode.debug.registerDebugAdapterTrackerFactory('prdb', {
            createDebugAdapterTracker: (session) => ({
                onDidSendMessage: (message) => {
                    if (message.type === 'event' && message.event === 'stopped') {
                        stopped(session);
                    }
                },
            }),
        }),
        vscode.debug.onDidTerminateDebugSession((session) => {
            if (panel && shown?.id === session.id) {
                shown = undefined;
                panel.title = 'Trace Diagram (ended)';
                panel.webview.postMessage({ type: 'ended' });
            }
        }),
    );
}

// showDiagram opens the panel on session's trace, or shows it there.
async function showDiagram(session) {
    if (session?.type !== 'prdb') {
        vscode.window.showErrorMessage('Start debugging a P trace to see its diagram.');
        return;
    }
    let body;
    try {
        body = await session.customRequest('prdbDiagram');
    } catch (e) {
        vscode.window.showErrorMessage(`prdb couldn't draw the trace: ${e.message}`);
        return;
    }
    if (!panel) {
        panel = vscode.window.createWebviewPanel(
            'prdbDiagram',
            'Trace Diagram',
            { viewColumn: vscode.ViewColumn.Beside, preserveFocus: true },
            { enableScripts: true, retainContextWhenHidden: true },
        );
        panel.onDidDispose(() => {
            panel = undefined;
            shown = undefined;
        });
        panel.webview.onDidReceiveMessage((message) => {
            if (message.type === 'goto' && shown) {
                // A failure is the debugger's to report; the panel just stays.
                shown.customRequest('prdbGoto', { step: message.step }).then(undefined, () => {});
            }
        });
    }
    shown = session;
    panel.title = 'Trace Diagram';
    panel.webview.html = page(body.svg, body.step);
    panel.reveal(undefined, true);
}

// stopped moves the highlight to where session stopped, or, if the panel
// shows another session's trace, shows this one's.
async function stopped(session) {
    if (!panel) {
        return;
    }
    if (shown?.id !== session.id) {
        await showDiagram(session);
        return;
    }
    try {
        const { step } = await session.customRequest('prdbStep');
        panel?.webview.postMessage({ type: 'step', step });
    } catch {
        // The session ended meanwhile.
    }
}

// page is the panel's HTML: the diagram in the editor's colours, and a
// script that highlights the current step and sends clicks back.
function page(svg, step) {
    const nonce = crypto.randomBytes(16).toString('base64');
    return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';">
<style>
body { margin: 0; padding: 0; background: var(--vscode-editor-background); }
/* The editor's colours, over the diagram's own light and dark ones. */
body svg {
    --bg: var(--vscode-editor-background);
    --fg: var(--vscode-editor-foreground);
    --muted: var(--vscode-descriptionForeground);
    --line: var(--vscode-editorIndentGuide-background1, var(--vscode-panel-border));
    --band: var(--vscode-list-hoverBackground);
    --accent: var(--vscode-textLink-foreground);
    --mon: var(--vscode-charts-purple);
    --fail: var(--vscode-errorForeground);
    --fail-bg: var(--vscode-inputValidation-errorBackground);
    --pill: var(--vscode-editor-selectionBackground);
}
body .row { cursor: pointer; }
body .row:hover { fill: var(--vscode-list-hoverBackground); }
body .row.current { fill: var(--vscode-editor-stackFrameHighlightBackground); }
body .step { cursor: pointer; }
body.ended .row, body.ended .step { cursor: default; }
</style>
</head>
<body>
${svg}
<script nonce="${nonce}">
const vscode = acquireVsCodeApi();
const rows = [...document.querySelectorAll('.row')];

// mark highlights the step's row, or for a step with no row, like leaving
// a state, the row before it, and scrolls it into view if it's out.
function mark(step) {
    let row;
    for (const r of rows) {
        if (Number(r.dataset.step) > step) break;
        row = r;
    }
    document.querySelector('.row.current')?.classList.remove('current');
    if (!row) return;
    row.classList.add('current');
    const box = row.getBoundingClientRect();
    if (box.top < 0 || box.bottom > window.innerHeight) {
        row.scrollIntoView({ block: 'center' });
    }
}

document.addEventListener('click', (e) => {
    const el = e.target.closest('[data-step]');
    if (el && !document.body.classList.contains('ended')) {
        vscode.postMessage({ type: 'goto', step: Number(el.dataset.step) });
    }
});
window.addEventListener('message', (e) => {
    if (e.data.type === 'step') mark(e.data.step);
    if (e.data.type === 'ended') document.body.classList.add('ended');
});
mark(${Number(step)});
</script>
</body>
</html>`;
}

module.exports = { registerDiagram, showDiagram, page };
