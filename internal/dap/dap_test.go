package dap

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	godap "github.com/google/go-dap"
)

// client plays the editor's side of the protocol.
type client struct {
	t        *testing.T
	w        io.Writer
	messages chan godap.Message
	seq      int
	// events are the events received while waiting for responses.
	events []godap.Message
}

func connect(t *testing.T) *client {
	t.Helper()
	toServer, fromClient := io.Pipe()
	toClient, fromServer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- Serve(toServer, fromServer, nil)
		fromServer.Close()
	}()
	c := &client{t: t, w: fromClient, messages: make(chan godap.Message, 100)}
	go func() {
		r := bufio.NewReader(toClient)
		for {
			msg, err := godap.ReadProtocolMessage(r)
			if err != nil {
				close(c.messages)
				return
			}
			c.messages <- msg
		}
	}()
	t.Cleanup(func() {
		fromClient.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("server: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server didn't stop")
		}
	})
	return c
}

// request sends a request and returns its response, keeping the events
// that come before it.
func (c *client) request(req godap.RequestMessage) godap.ResponseMessage {
	c.t.Helper()
	c.seq++
	r := req.GetRequest()
	r.Seq, r.Type = c.seq, "request"
	if err := godap.WriteProtocolMessage(c.w, req); err != nil {
		c.t.Fatal(err)
	}
	for {
		select {
		case msg, ok := <-c.messages:
			if !ok {
				c.t.Fatalf("%s: the server closed the connection", r.Command)
			}
			if resp, ok := msg.(godap.ResponseMessage); ok && resp.GetResponse().RequestSeq == c.seq {
				if !resp.GetResponse().Success {
					c.t.Fatalf("%s failed: %s", r.Command, resp.GetResponse().Message)
				}
				return resp
			}
			c.events = append(c.events, msg)
		case <-time.After(5 * time.Second):
			c.t.Fatalf("%s: no response", r.Command)
		}
	}
}

// stopped waits for the next stopped event.
func (c *client) stopped() godap.StoppedEventBody {
	c.t.Helper()
	for {
		for i, e := range c.events {
			if s, ok := e.(*godap.StoppedEvent); ok {
				c.events = append(c.events[:i:i], c.events[i+1:]...)
				return s.Body
			}
		}
		select {
		case msg, ok := <-c.messages:
			if !ok {
				c.t.Fatal("the server closed the connection")
			}
			c.events = append(c.events, msg)
		case <-time.After(5 * time.Second):
			c.t.Fatal("no stopped event")
		}
	}
}

// output returns the text of the output events received, by category.
func (c *client) output(category string) string {
	var b strings.Builder
	for _, e := range c.events {
		if o, ok := e.(*godap.OutputEvent); ok && o.Body.Category == category {
			b.WriteString(o.Body.Output)
		}
	}
	return b.String()
}

func (c *client) variables(ref int) map[string]godap.Variable {
	c.t.Helper()
	resp := c.request(&godap.VariablesRequest{Request: godap.Request{Command: "variables"}, Arguments: godap.VariablesArguments{VariablesReference: ref}})
	vars := map[string]godap.Variable{}
	for _, v := range resp.(*godap.VariablesResponse).Body.Variables {
		vars[v.Name] = v
	}
	return vars
}

func TestSession(t *testing.T) {
	c := connect(t)
	fixture, _ := filepath.Abs("../../testdata/pingpong")
	pingPong := filepath.Join(fixture, "PSrc", "PingPong.p")

	init := c.request(&godap.InitializeRequest{Request: godap.Request{Command: "initialize"}}).(*godap.InitializeResponse)
	if !init.Body.SupportsStepBack || !init.Body.SupportsConfigurationDoneRequest {
		t.Errorf("capabilities %+v", init.Body)
	}

	// Breakpoints can be set before the trace is loaded.
	c.request(&godap.SetBreakpointsRequest{Request: godap.Request{Command: "setBreakpoints"}, Arguments: godap.SetBreakpointsArguments{
		Source: godap.Source{Path: pingPong}, Breakpoints: []godap.SourceBreakpoint{{Line: 21}},
	}})
	args, _ := json.Marshal(LaunchArguments{Trace: filepath.Join(fixture, "bug.trace.json")})
	c.request(&godap.LaunchRequest{Request: godap.Request{Command: "launch"}, Arguments: args})
	c.request(&godap.ConfigurationDoneRequest{Request: godap.Request{Command: "configurationDone"}})
	// The first stop comes twice, for Helix; see stopped.
	for range 2 {
		if s := c.stopped(); s.Reason != "entry" || !s.AllThreadsStopped {
			t.Errorf("first stop %+v", s)
		}
	}

	// Breakpoints are checked against the trace once it's loaded.
	resp := c.request(&godap.SetBreakpointsRequest{Request: godap.Request{Command: "setBreakpoints"}, Arguments: godap.SetBreakpointsArguments{
		Source: godap.Source{Path: pingPong}, Breakpoints: []godap.SourceBreakpoint{{Line: 21}, {Line: 25}},
	}}).(*godap.SetBreakpointsResponse)
	if b := resp.Body.Breakpoints; len(b) != 2 || !b[0].Verified || b[1].Verified {
		t.Errorf("breakpoints %+v", b)
	}

	// Continue to the server's send of ePong. Later stops come once.
	c.request(&godap.ContinueRequest{Request: godap.Request{Command: "continue"}})
	stop := c.stopped()
	if stop.Reason != "breakpoint" {
		t.Errorf("stopped for %q", stop.Reason)
	}
	for _, e := range c.events {
		if _, ok := e.(*godap.StoppedEvent); ok {
			t.Error("a later stop came twice")
		}
	}
	threads := c.request(&godap.ThreadsRequest{Request: godap.Request{Command: "threads"}}).(*godap.ThreadsResponse).Body.Threads
	names := map[int]string{}
	for _, th := range threads {
		names[th.Id] = th.Name
	}
	if len(threads) != 4 || names[stop.ThreadId] != "Server(3)" {
		t.Errorf("threads %+v, stopped in %d", threads, stop.ThreadId)
	}

	frames := c.request(&godap.StackTraceRequest{Request: godap.Request{Command: "stackTrace"}, Arguments: godap.StackTraceArguments{ThreadId: stop.ThreadId}}).(*godap.StackTraceResponse).Body.StackFrames
	if len(frames) != 2 || frames[0].Name != "Serving: send ePong to Client(2)" || frames[0].Line != 21 || frames[0].Source.Path != pingPong ||
		frames[1].Name != "machine Server(3)" || frames[1].Line != 8 {
		t.Errorf("frames %+v", frames)
	}

	scopes := c.request(&godap.ScopesRequest{Request: godap.Request{Command: "scopes"}, Arguments: godap.ScopesArguments{FrameId: frames[0].Id}}).(*godap.ScopesResponse).Body.Scopes
	if len(scopes) != 2 {
		t.Fatalf("scopes %+v", scopes)
	}
	step := c.variables(scopes[0].VariablesReference)
	if step["event"].Value != "ePong" || step["payload"].Value != "1" || step["target"].Value != "Client(2)" {
		t.Errorf("step variables %+v", step)
	}
	machine := c.variables(scopes[1].VariablesReference)
	if machine["state"].Value != "Serving" || machine["inbox"].Value != "0 waiting" {
		t.Errorf("machine variables %+v", machine)
	}

	// The client has the pong waiting for it.
	var client int
	for id, name := range names {
		if name == "Client(2)" {
			client = id
		}
	}
	clientFrames := c.request(&godap.StackTraceRequest{Request: godap.Request{Command: "stackTrace"}, Arguments: godap.StackTraceArguments{ThreadId: client}}).(*godap.StackTraceResponse).Body.StackFrames
	clientScopes := c.request(&godap.ScopesRequest{Request: godap.Request{Command: "scopes"}, Arguments: godap.ScopesArguments{FrameId: clientFrames[0].Id}}).(*godap.ScopesResponse).Body.Scopes
	inbox := c.variables(c.variables(clientScopes[1].VariablesReference)["inbox"].VariablesReference)
	if len(inbox) != 1 || inbox["0"].Value != "ePong from Server(3): 1" {
		t.Errorf("client inbox %+v", inbox)
	}

	// Run to the failed assertion, with no breakpoints.
	c.request(&godap.SetBreakpointsRequest{Request: godap.Request{Command: "setBreakpoints"}, Arguments: godap.SetBreakpointsArguments{Source: godap.Source{Path: pingPong}}})
	c.request(&godap.ContinueRequest{Request: godap.Request{Command: "continue"}})
	stop = c.stopped()
	if stop.Reason != "exception" || !strings.Contains(stop.Text, "expected 3 pongs, got 4") || names[stop.ThreadId] != "Client(2)" {
		t.Errorf("stopped %+v", stop)
	}
	info := c.request(&godap.ExceptionInfoRequest{Request: godap.Request{Command: "exceptionInfo"}, Arguments: godap.ExceptionInfoArguments{ThreadId: stop.ThreadId}}).(*godap.ExceptionInfoResponse)
	if !strings.Contains(info.Body.Description, "PSrc/PingPong.p:46:13") {
		t.Errorf("exception info %+v", info.Body)
	}
	frames = c.request(&godap.StackTraceRequest{Request: godap.Request{Command: "stackTrace"}, Arguments: godap.StackTraceArguments{ThreadId: stop.ThreadId}}).(*godap.StackTraceResponse).Body.StackFrames
	if frames[0].Line != 46 || frames[0].Column != 13 {
		t.Errorf("assertion frame %+v", frames[0])
	}
	if out := c.output("stdout"); out != "[22] got pong 1\n[31] got pong 2\n[40] got pong 4\n" {
		t.Errorf("program output %q", out)
	}

	// Stepping past the failure stays on it.
	c.request(&godap.StepInRequest{Request: godap.Request{Command: "stepIn"}, Arguments: godap.StepInArguments{ThreadId: stop.ThreadId}})
	if again := c.stopped(); again.Reason != "exception" || names[again.ThreadId] != "Client(2)" {
		t.Errorf("stepping past the failure %+v", again)
	}

	// Step the client back, to its print.
	c.request(&godap.StepBackRequest{Request: godap.Request{Command: "stepBack"}, Arguments: godap.StepBackArguments{ThreadId: stop.ThreadId}})
	c.stopped()
	frames = c.request(&godap.StackTraceRequest{Request: godap.Request{Command: "stackTrace"}, Arguments: godap.StackTraceArguments{ThreadId: stop.ThreadId}}).(*godap.StackTraceResponse).Body.StackFrames
	if frames[0].Name != "Pinging: print" || frames[0].Line != 45 {
		t.Errorf("after stepping back %+v", frames[0])
	}

	c.request(&godap.DisconnectRequest{Request: godap.Request{Command: "disconnect"}})
}

func TestLaunchErrors(t *testing.T) {
	c := connect(t)
	c.request(&godap.InitializeRequest{Request: godap.Request{Command: "initialize"}})
	for _, args := range []string{`{}`, `{"trace": "no-such-file.json"}`} {
		c.seq++
		req := &godap.LaunchRequest{Request: godap.Request{ProtocolMessage: godap.ProtocolMessage{Seq: c.seq, Type: "request"}, Command: "launch"}, Arguments: json.RawMessage(args)}
		godap.WriteProtocolMessage(c.w, req)
		msg := <-c.messages
		if resp, ok := msg.(godap.ResponseMessage); !ok || resp.GetResponse().Success {
			t.Errorf("launch %s: %s", args, fmt.Sprint(msg))
		}
	}
}

// VS Code sends its own fields with the launch arguments, and a trace path
// relative to the workspace folder, which it runs prdb in.
func TestLaunchFromVSCode(t *testing.T) {
	t.Chdir("../../testdata/pingpong")
	c := connect(t)
	c.request(&godap.InitializeRequest{Request: godap.Request{Command: "initialize"}})
	args := json.RawMessage(`{"type": "prdb", "request": "launch", "name": "Replay a P trace",
		"trace": "bug.trace.json", "__sessionId": "1234", "__configurationTarget": 6}`)
	c.request(&godap.LaunchRequest{Request: godap.Request{Command: "launch"}, Arguments: args})
	c.request(&godap.ConfigurationDoneRequest{Request: godap.Request{Command: "configurationDone"}})
	if s := c.stopped(); s.Reason != "entry" {
		t.Errorf("stopped %+v", s)
	}
	c.request(&godap.DisconnectRequest{Request: godap.Request{Command: "disconnect"}})
}

func TestLaunchSchedule(t *testing.T) {
	if _, err := exec.LookPath("p"); err != nil {
		t.Skip("the P compiler isn't installed")
	}
	project := t.TempDir()
	if err := os.CopyFS(project, os.DirFS("../../testdata/pingpong")); err != nil {
		t.Fatal(err)
	}
	c := connect(t)
	c.request(&godap.InitializeRequest{Request: godap.Request{Command: "initialize"}})
	args, _ := json.Marshal(LaunchArguments{Trace: filepath.Join(project, "bug.schedule")})
	c.request(&godap.LaunchRequest{Request: godap.Request{Command: "launch"}, Arguments: args})
	c.request(&godap.ConfigurationDoneRequest{Request: godap.Request{Command: "configurationDone"}})
	if s := c.stopped(); s.Reason != "entry" {
		t.Errorf("stopped %+v", s)
	}
	c.stopped() // the first stop is sent twice, for Helix
	c.request(&godap.ContinueRequest{Request: godap.Request{Command: "continue"}})
	if s := c.stopped(); s.Reason != "exception" || !strings.Contains(s.Text, "expected 3 pongs, got 4") {
		t.Errorf("stopped %+v", s)
	}
	if out := c.output("console"); !strings.Contains(out, "Compiling PingPong.pproj\nReplaying bug.schedule\n") {
		t.Errorf("console %q", out)
	}
	c.request(&godap.DisconnectRequest{Request: godap.Request{Command: "disconnect"}})
}
