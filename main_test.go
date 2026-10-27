package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestMain runs this test binary as the server when a test starts it with
// P_LSP_TEST_SERVER set, so tests can talk to the real thing over stdio.
func TestMain(m *testing.M) {
	if os.Getenv("P_LSP_TEST_SERVER") != "" {
		os.Args = append([]string{os.Args[0]}, strings.Fields(os.Getenv("P_LSP_TEST_SERVER"))...)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// message is a JSON-RPC message in either direction.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// lspClient drives a server process the way an editor would.
type lspClient struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	nextID int
	// responses carries the server's responses, and notifications its
	// notifications. The server's own requests are answered with null and
	// recorded in requests.
	responses     chan message
	notifications chan message
	requests      chan message
}

func startServer(t *testing.T, args string) *lspClient {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "P_LSP_TEST_SERVER=-stdio "+args)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &lspClient{
		t: t, cmd: cmd, stdin: stdin,
		responses:     make(chan message, 10),
		notifications: make(chan message, 100),
		requests:      make(chan message, 10),
	}
	t.Cleanup(func() {
		stdin.Close()
		cmd.Process.Kill()
		cmd.Wait()
	})
	go c.read(bufio.NewReader(stdout))
	return c
}

func (c *lspClient) read(r *bufio.Reader) {
	for {
		length := 0
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if v, ok := strings.CutPrefix(line, "Content-Length: "); ok {
				length, _ = strconv.Atoi(v)
			}
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(r, body); err != nil {
			return
		}
		var m message
		if err := json.Unmarshal(body, &m); err != nil {
			c.t.Errorf("server sent invalid JSON: %s", body)
			return
		}
		switch {
		case m.Method != "" && m.ID != nil:
			c.requests <- m
			c.send(map[string]any{"jsonrpc": "2.0", "id": *m.ID, "result": nil})
		case m.Method != "":
			c.notifications <- m
		default:
			c.responses <- m
		}
	}
}

func (c *lspClient) send(msg any) {
	body, err := json.Marshal(msg)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
		c.t.Errorf("writing to the server: %v", err)
	}
}

func (c *lspClient) notify(method string, params any) {
	c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// request sends a request and decodes its result into result.
func (c *lspClient) request(method string, params, result any) {
	c.t.Helper()
	c.nextID++
	id := c.nextID
	c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	select {
	case m := <-c.responses:
		if m.ID == nil || *m.ID != id {
			c.t.Fatalf("%s: got a response to request %v", method, m.ID)
		}
		if m.Error != nil {
			c.t.Fatalf("%s: %s", method, m.Error.Message)
		}
		if result != nil {
			if err := json.Unmarshal(m.Result, result); err != nil {
				c.t.Fatalf("%s: decoding %s: %v", method, m.Result, err)
			}
		}
	case <-time.After(5 * time.Second):
		c.t.Fatalf("%s: no response", method)
	}
}

// diagnostics waits for the next diagnostics the server publishes for uri,
// and returns their messages.
func (c *lspClient) diagnostics(uri string) []string {
	c.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-c.notifications:
			if m.Method != "textDocument/publishDiagnostics" {
				continue
			}
			var p struct {
				URI         string `json:"uri"`
				Diagnostics []struct {
					Message string `json:"message"`
				} `json:"diagnostics"`
			}
			json.Unmarshal(m.Params, &p)
			if p.URI != uri {
				continue
			}
			messages := []string{}
			for _, d := range p.Diagnostics {
				messages = append(messages, d.Message)
			}
			return messages
		case <-deadline:
			c.t.Fatalf("no diagnostics published for %s", uri)
		}
	}
}

func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type location struct {
	URI   string `json:"uri"`
	Range struct {
		Start position `json:"start"`
	} `json:"range"`
}

func textDocument(uri string, pos position) map[string]any {
	return map[string]any{"textDocument": map[string]any{"uri": uri}, "position": pos}
}

const (
	eventsP = "// A request.\nevent eReq: (amount: int);\n"
	clientP = `machine Client {
  var count: int;
  start state Init {
    on eReq do (req: (amount: int)) {
      count = req.amount;
    }
  }
}
`
)

// TestProtocol runs a session against the server binary: the editor's view
// of everything, from the capabilities it advertises to the JSON each
// request returns.
func TestProtocol(t *testing.T) {
	root := t.TempDir()
	for name, text := range map[string]string{
		"App.pproj":         `<Project><InputFiles><PFile>./PSrc/</PFile></InputFiles></Project>`,
		"PSrc/Events.p":     eventsP,
		"PSrc/Client.p":     clientP,
		"Other/Other.p":     "event eUnrelated;\n",
		"Other/Other.pproj": `<Project><InputFiles><PFile>./Other.p</PFile></InputFiles></Project>`,
	} {
		path := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	events, client := fileURI(filepath.Join(root, "PSrc/Events.p")), fileURI(filepath.Join(root, "PSrc/Client.p"))

	c := startServer(t, "-compiler=")

	var init struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
		ServerInfo   struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	c.request("initialize", map[string]any{
		"processId": nil,
		"rootUri":   fileURI(root),
		"capabilities": map[string]any{"workspace": map[string]any{
			"didChangeWatchedFiles": map[string]any{"dynamicRegistration": true},
		}},
	}, &init)
	for capability, want := range map[string]string{
		"textDocumentSync":        `"change":1`,
		"completionProvider":      `"triggerCharacters":["."]`,
		"renameProvider":          `"prepareProvider":true`,
		"semanticTokensProvider":  `"tokenTypes":["namespace"`,
		"definitionProvider":      `true`,
		"referencesProvider":      `true`,
		"hoverProvider":           `true`,
		"documentSymbolProvider":  `true`,
		"workspaceSymbolProvider": `true`,
		"foldingRangeProvider":    `true`,
		"selectionRangeProvider":  `true`,
		"codeActionProvider":      `"codeActionKinds":["quickfix"]`,
	} {
		if got := string(init.Capabilities[capability]); !strings.Contains(got, want) {
			t.Errorf("capability %s = %s, want it to contain %s", capability, got, want)
		}
	}
	if init.ServerInfo.Name != "p-lsp" {
		t.Errorf("server name %q", init.ServerInfo.Name)
	}

	c.notify("initialized", map[string]any{})
	select {
	case r := <-c.requests:
		if r.Method != "client/registerCapability" || !strings.Contains(string(r.Params), "workspace/didChangeWatchedFiles") {
			t.Errorf("server asked %s %s, want file watchers registered", r.Method, r.Params)
		}
	case <-time.After(5 * time.Second):
		t.Error("server didn't register file watchers")
	}

	// Opening a file with a syntax error reports it; fixing it clears it.
	c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
		"uri": client, "languageId": "p", "version": 1, "text": clientP + "event\n",
	}})
	if got := c.diagnostics(client); len(got) != 1 {
		t.Errorf("after opening with a syntax error, diagnostics %v", got)
	}
	c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": client, "version": 2},
		"contentChanges": []any{map[string]any{"text": clientP}},
	})
	if got := c.diagnostics(client); len(got) != 0 {
		t.Errorf("after fixing it, diagnostics %v", got)
	}

	eReq := position{Line: 3, Character: 8} // in Client.p's "on eReq"

	var defs []location
	c.request("textDocument/definition", textDocument(client, eReq), &defs)
	if len(defs) != 1 || defs[0].URI != events || defs[0].Range.Start != (position{1, 6}) {
		t.Errorf("definition %+v, want Events.p 2:7", defs)
	}

	var refs []location
	c.request("textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": client}, "position": eReq,
		"context": map[string]any{"includeDeclaration": true},
	}, &refs)
	if len(refs) != 2 {
		t.Errorf("references %+v, want the declaration and the handler", refs)
	}

	var hover struct {
		Contents struct {
			Value string `json:"value"`
		} `json:"contents"`
	}
	c.request("textDocument/hover", textDocument(client, eReq), &hover)
	if v := hover.Contents.Value; !strings.Contains(v, "event eReq: (amount: int);") || !strings.Contains(v, "A request.") {
		t.Errorf("hover %q", v)
	}

	// Fields after "req." in "count = req.amount;".
	var items []struct {
		Label string `json:"label"`
	}
	c.request("textDocument/completion", textDocument(client, position{4, 18}), &items)
	if len(items) != 1 || items[0].Label != "amount" {
		t.Errorf("completion %+v, want amount", items)
	}

	var rng struct {
		Start position `json:"start"`
		End   position `json:"end"`
	}
	c.request("textDocument/prepareRename", textDocument(client, eReq), &rng)
	if rng.Start != (position{3, 7}) || rng.End != (position{3, 11}) {
		t.Errorf("prepareRename %+v", rng)
	}
	var edit struct {
		Changes map[string][]struct {
			NewText string `json:"newText"`
		} `json:"changes"`
	}
	params := textDocument(client, eReq)
	params["newName"] = "eRequest"
	c.request("textDocument/rename", params, &edit)
	if len(edit.Changes[events]) != 1 || len(edit.Changes[client]) != 1 || len(edit.Changes) != 2 {
		t.Errorf("rename edits %+v, want one in each file", edit.Changes)
	}

	// Renaming to a keyword fails with a message for the user.
	c.nextID++
	c.send(map[string]any{"jsonrpc": "2.0", "id": c.nextID, "method": "textDocument/rename",
		"params": map[string]any{"textDocument": map[string]any{"uri": client}, "position": eReq, "newName": "machine"}})
	if m := <-c.responses; m.Error == nil || !strings.Contains(m.Error.Message, "keyword") {
		t.Errorf("rename to a keyword: %s %+v", m.Result, m.Error)
	}

	var symbols []struct {
		Name string `json:"name"`
	}
	c.request("textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": client}}, &symbols)
	if len(symbols) != 1 || symbols[0].Name != "Client" {
		t.Errorf("document symbols %+v", symbols)
	}
	var found []struct {
		Name     string   `json:"name"`
		Location location `json:"location"`
	}
	c.request("workspace/symbol", map[string]any{"query": "eR"}, &found)
	names := []string{}
	for _, s := range found {
		names = append(names, s.Name)
	}
	if !slices.Contains(names, "eReq") {
		t.Errorf("workspace symbols for eR: %v", names)
	}

	var folds []struct {
		StartLine int `json:"startLine"`
		EndLine   int `json:"endLine"`
	}
	c.request("textDocument/foldingRange", map[string]any{"textDocument": map[string]any{"uri": client}}, &folds)
	if len(folds) != 3 || folds[0].StartLine != 0 || folds[0].EndLine != 6 {
		t.Errorf("folding ranges %+v", folds)
	}

	var selections []struct {
		Parent *json.RawMessage `json:"parent"`
	}
	c.request("textDocument/selectionRange", map[string]any{
		"textDocument": map[string]any{"uri": client}, "positions": []position{eReq},
	}, &selections)
	if len(selections) != 1 || selections[0].Parent == nil {
		t.Errorf("selection ranges %+v", selections)
	}

	var tokens struct {
		Data []int `json:"data"`
	}
	c.request("textDocument/semanticTokens/full", map[string]any{"textDocument": map[string]any{"uri": client}}, &tokens)
	if len(tokens.Data) == 0 || len(tokens.Data)%5 != 0 {
		t.Errorf("semantic tokens %v", tokens.Data)
	}

	// Closing a file reverts it to the disk version and clears its errors.
	c.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": client}})
	if got := c.diagnostics(client); len(got) != 0 {
		t.Errorf("after closing, diagnostics %v", got)
	}

	c.request("shutdown", nil, nil)
	c.notify("exit", nil)
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("server exited with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("server didn't exit")
	}
}

func TestVersionFlag(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "P_LSP_TEST_SERVER=-version")
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != version {
		t.Errorf("-version printed %q, %v", out, err)
	}
}
