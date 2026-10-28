// Package dap serves a replay session over the Debug Adapter Protocol.
//
// Each machine is a thread. Stepping moves through the trace's steps:
// next goes to the selected machine's next step, step in to the next step
// of any machine, step out past the end of the machine's current handler,
// and step back and reverse continue go the other way. Breakpoints stop at
// steps on their line, or at the start of a handler they're in.
package dap

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/bencornia/prdb/internal/session"
	"github.com/bencornia/prdb/internal/source"
	"github.com/bencornia/prdb/internal/trace"
	godap "github.com/google/go-dap"
)

// LaunchArguments are what a launch request configures.
type LaunchArguments struct {
	// Trace is the PChecker trace to step through, a .trace.json file, or
	// a .schedule file, which is compiled and replayed into a trace.
	Trace string `json:"trace"`
	// Project is the P project's directory. By default it's the nearest
	// directory, from the trace's up, with a .pproj file.
	Project string `json:"project"`
	// StopOnEntry stops at the first step. It's true unless set false;
	// otherwise the session runs to the first breakpoint.
	StopOnEntry *bool `json:"stopOnEntry"`
	// TestCase, in place of Trace, is a test case to check until PChecker
	// finds a bug, and then debug the bug's trace.
	TestCase string `json:"testcase"`
	// Schedules is how many schedules to explore for a bug in TestCase:
	// 1000 by default.
	Schedules int `json:"schedules"`
	// Compiler runs the P tool, to replay a schedule or check a test case;
	// "p" by default.
	Compiler []string `json:"compiler"`
}

// Serve speaks the protocol on r and w until the client disconnects. If
// log isn't nil, every message is copied to it.
func Serve(r io.Reader, w io.Writer, log io.Writer) error {
	s := &server{in: bufio.NewReader(r), out: w, log: log, pendingBreakpoints: map[string][]int{}}
	defer s.close()
	for {
		msg, err := godap.ReadProtocolMessage(s.in)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		s.logf("<- %+v", msg)
		if done := s.handle(msg); done {
			return nil
		}
	}
}

type server struct {
	in  *bufio.Reader
	log io.Writer

	mu  sync.Mutex
	out io.Writer
	seq int

	session      *session.Session
	closeSession func()
	stopOnEntry  bool
	// pendingBreakpoints are set before launch, by file.
	pendingBreakpoints map[string][]int
	// threads numbers the machines, from 1.
	threads map[string]int
	// variables holds what each variables reference expands to, until the
	// session moves.
	variables map[int][]godap.Variable
	// stoppedBefore reports whether a stopped event has been sent.
	stoppedBefore bool
}

func (s *server) close() {
	if s.closeSession != nil {
		s.closeSession()
	}
}

func (s *server) logf(format string, args ...any) {
	if s.log != nil {
		fmt.Fprintf(s.log, format+"\n", args...)
	}
}

func (s *server) send(msg godap.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	switch m := msg.(type) {
	case godap.ResponseMessage:
		m.GetResponse().Seq = s.seq
		m.GetResponse().Type = "response"
	case godap.EventMessage:
		m.GetEvent().Seq = s.seq
		m.GetEvent().Type = "event"
	}
	if s.log != nil {
		data, _ := json.Marshal(msg)
		s.logf("-> %s", data)
	}
	if err := godap.WriteProtocolMessage(s.out, msg); err != nil {
		s.logf("writing: %v", err)
	}
}

func response(req godap.RequestMessage) godap.Response {
	r := req.GetRequest()
	return godap.Response{RequestSeq: r.Seq, Command: r.Command, Success: true}
}

func event(name string) godap.Event {
	return godap.Event{Event: name}
}

func (s *server) fail(req godap.RequestMessage, message string) {
	r := response(req)
	r.Success = false
	r.Message = message
	s.send(&godap.ErrorResponse{Response: r, Body: godap.ErrorResponseBody{Error: &godap.ErrorMessage{Format: message, ShowUser: true}}})
}

// handle answers a request, and reports whether the session is over.
func (s *server) handle(msg godap.Message) bool {
	req, ok := msg.(godap.RequestMessage)
	if !ok {
		return false
	}
	if s.session == nil {
		switch msg.(type) {
		case *godap.InitializeRequest, *godap.LaunchRequest, *godap.SetBreakpointsRequest,
			*godap.DisconnectRequest, *godap.TerminateRequest, *godap.ConfigurationDoneRequest:
		default:
			s.fail(req, "no trace is loaded: launch first")
			return false
		}
	}

	switch m := msg.(type) {
	case *godap.InitializeRequest:
		s.send(&godap.InitializeResponse{Response: response(req), Body: godap.Capabilities{
			SupportsConfigurationDoneRequest: true,
			SupportsStepBack:                 true,
			SupportsExceptionInfoRequest:     true,
			SupportsTerminateRequest:         true,
			SupportsRestartRequest:           true,
		}})
	case *godap.LaunchRequest:
		s.launch(m)
	case *godap.SetBreakpointsRequest:
		s.setBreakpoints(m)
	case *godap.ConfigurationDoneRequest:
		s.send(&godap.ConfigurationDoneResponse{Response: response(req)})
		if s.session == nil {
			return false
		}
		if s.stopOnEntry {
			s.stopped("entry")
		} else {
			s.move(s.session.Continue)
		}
	case *godap.ThreadsRequest:
		s.send(&godap.ThreadsResponse{Response: response(req), Body: godap.ThreadsResponseBody{Threads: s.threadList()}})
	case *godap.StackTraceRequest:
		frames := s.stackTrace(m.Arguments.ThreadId)
		s.send(&godap.StackTraceResponse{Response: response(req), Body: godap.StackTraceResponseBody{StackFrames: frames, TotalFrames: len(frames)}})
	case *godap.ScopesRequest:
		s.send(&godap.ScopesResponse{Response: response(req), Body: godap.ScopesResponseBody{Scopes: s.scopes(m.Arguments.FrameId)}})
	case *godap.VariablesRequest:
		vars := s.variables[m.Arguments.VariablesReference]
		if vars == nil {
			vars = []godap.Variable{}
		}
		s.send(&godap.VariablesResponse{Response: response(req), Body: godap.VariablesResponseBody{Variables: vars}})

	case *godap.ContinueRequest:
		s.send(&godap.ContinueResponse{Response: response(req), Body: godap.ContinueResponseBody{AllThreadsContinued: true}})
		s.move(s.session.Continue)
	case *godap.NextRequest:
		s.send(&godap.NextResponse{Response: response(req)})
		machine := s.machine(m.Arguments.ThreadId)
		s.move(func() bool { return s.session.Next(machine) })
	case *godap.StepInRequest:
		s.send(&godap.StepInResponse{Response: response(req)})
		s.move(s.session.StepIn)
	case *godap.StepOutRequest:
		s.send(&godap.StepOutResponse{Response: response(req)})
		machine := s.machine(m.Arguments.ThreadId)
		s.move(func() bool { return s.session.StepOut(machine) })
	case *godap.StepBackRequest:
		s.send(&godap.StepBackResponse{Response: response(req)})
		machine := s.machine(m.Arguments.ThreadId)
		s.move(func() bool { return s.session.StepBack(machine) })
	case *godap.ReverseContinueRequest:
		s.send(&godap.ReverseContinueResponse{Response: response(req)})
		s.move(s.session.ReverseContinue)
	case *godap.RestartRequest:
		s.send(&godap.RestartResponse{Response: response(req)})
		s.session.Pos = 0
		s.stopped("entry")
	case *godap.PauseRequest:
		// Nothing runs between requests, so it's always paused.
		s.send(&godap.PauseResponse{Response: response(req)})
	case *godap.ExceptionInfoRequest:
		step := s.session.Current()
		s.send(&godap.ExceptionInfoResponse{Response: response(req), Body: godap.ExceptionInfoResponseBody{
			ExceptionId: "AssertionFailure", Description: step.Error, BreakMode: "always",
		}})

	case *godap.DisconnectRequest:
		s.send(&godap.DisconnectResponse{Response: response(req)})
		return true
	case *godap.TerminateRequest:
		s.send(&godap.TerminateResponse{Response: response(req)})
		s.send(&godap.TerminatedEvent{Event: event("terminated")})
	default:
		s.fail(req, fmt.Sprintf("%s isn't supported", req.GetRequest().Command))
	}
	return false
}

func (s *server) launch(req *godap.LaunchRequest) {
	var args LaunchArguments
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		s.fail(req, "bad launch arguments: "+err.Error())
		return
	}
	if args.Trace == "" && args.TestCase == "" {
		s.fail(req, "launch needs a trace, the .trace.json file PChecker wrote, or a test case to check")
		return
	}
	sess, closeSession, err := session.Open(args.Trace, session.Options{
		Project: args.Project, Compiler: args.Compiler, Progress: consoleWriter{s},
		TestCase: args.TestCase, Schedules: args.Schedules,
	})
	if err != nil {
		s.fail(req, err.Error())
		return
	}
	s.session, s.closeSession = sess, closeSession
	s.stopOnEntry = args.StopOnEntry == nil || *args.StopOnEntry
	s.threads = map[string]int{}
	for i, m := range sess.AllMachines() {
		s.threads[m] = i + 1
	}
	for file, lines := range s.pendingBreakpoints {
		sess.SetBreakpoints(file, lines)
	}
	s.send(&godap.LaunchResponse{Response: response(req)})
	s.send(&godap.InitializedEvent{Event: event("initialized")})
	from := args.Trace
	if args.TestCase != "" {
		from = "the bug in " + args.TestCase
	}
	s.output("console", fmt.Sprintf("Replaying %d steps from %s\n", len(sess.Steps), from))
}

func (s *server) setBreakpoints(req *godap.SetBreakpointsRequest) {
	path := req.Arguments.Source.Path
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	lines := make([]int, len(req.Arguments.Breakpoints))
	for i, b := range req.Arguments.Breakpoints {
		lines[i] = b.Line
	}
	var verified []bool
	if s.session == nil {
		s.pendingBreakpoints[path] = lines
		verified = make([]bool, len(lines))
		for i := range verified {
			verified[i] = true
		}
	} else {
		verified = s.session.SetBreakpoints(path, lines)
	}
	breakpoints := make([]godap.Breakpoint, len(lines))
	for i, line := range lines {
		breakpoints[i] = godap.Breakpoint{Id: i + 1, Verified: verified[i], Line: line, Source: &req.Arguments.Source}
		if !verified[i] {
			breakpoints[i].Message = "no step of the trace stops here"
		}
	}
	s.send(&godap.SetBreakpointsResponse{Response: response(req), Body: godap.SetBreakpointsResponseBody{Breakpoints: breakpoints}})
}

// move runs a session movement, writes the steps it passed, and reports
// where it stopped, or that the trace is over if it couldn't move.
func (s *server) move(step func() bool) {
	from := s.session.Pos
	if !step() {
		switch {
		case s.session.Current().Kind == "AssertionFailure":
			// Stay on the failure: it's what the trace is for.
			s.output("console", "The trace ends at the failed assertion.\n")
			s.stopped("exception")
		case s.session.AtEnd():
			s.send(&godap.TerminatedEvent{Event: event("terminated")})
		default:
			s.stopped("entry")
		}
		return
	}
	to := s.session.Pos
	for i := from + 1; i <= to; i++ {
		s.writeStep(i)
	}
	reason := "step"
	switch {
	case s.session.Current().Kind == "AssertionFailure":
		reason = "exception"
	case s.session.HitsBreakpoint(to):
		reason = "breakpoint"
	}
	s.stopped(reason)
}

// writeStep writes a step to the debug console, or a print to the
// program's output.
func (s *server) writeStep(i int) {
	category := "console"
	if s.session.Steps[i].Kind == "Print" {
		category = "stdout"
	}
	s.output(category, fmt.Sprintf("[%d] %s\n", i+1, s.session.Steps[i].Log))
}

func (s *server) output(category, text string) {
	s.send(&godap.OutputEvent{Event: event("output"), Body: godap.OutputEventBody{Category: category, Output: text}})
}

// consoleWriter writes to the debug console.
type consoleWriter struct{ s *server }

func (w consoleWriter) Write(p []byte) (int, error) {
	w.s.output("console", string(p))
	return len(p), nil
}

func (s *server) stopped(reason string) {
	s.variables = map[int][]godap.Variable{}
	step := s.session.Current()
	body := godap.StoppedEventBody{
		Reason:            reason,
		ThreadId:          s.threads[step.Machine],
		AllThreadsStopped: true,
		Description:       fmt.Sprintf("step %d of %d", s.session.Pos+1, len(s.session.Steps)),
	}
	if reason == "exception" {
		body.Description = "Assertion failed"
		body.Text = step.Error
	}
	s.send(&godap.StoppedEvent{Event: event("stopped"), Body: body})
	if !s.stoppedBefore {
		// Helix 25.07 only selects the stopped thread, and shows where it
		// is, once the session is active, and marks it active after
		// handling its first stopped event. Other clients take the repeat
		// as the same stop.
		s.stoppedBefore = true
		s.send(&godap.StoppedEvent{Event: event("stopped"), Body: body})
	}
}

// machine returns the machine a thread is.
func (s *server) machine(thread int) string {
	for m, id := range s.threads {
		if id == thread {
			return m
		}
	}
	return ""
}

func (s *server) threadList() []godap.Thread {
	threads := []godap.Thread{}
	for _, m := range s.session.Machines() {
		threads = append(threads, godap.Thread{Id: s.threads[m], Name: m})
	}
	sort.Slice(threads, func(i, j int) bool { return threads[i].Id < threads[j].Id })
	return threads
}

// Frame ids encode the thread and which of its frames.
const framesPerThread = 10

// stackTrace returns a machine's frames: where its latest step happened,
// and its declaration.
func (s *server) stackTrace(thread int) []godap.StackFrame {
	machine := s.machine(thread)
	i := s.session.LastStep(machine)
	if i < 0 {
		return []godap.StackFrame{}
	}
	step := s.session.Steps[i]
	name := describe(step)
	if state := s.session.State(machine); state != "" {
		name = state + ": " + name
	}
	frames := []godap.StackFrame{frame(thread*framesPerThread, name, s.session.Where[i])}
	if m := s.session.Source.Machines[step.MachineType()]; m != nil {
		kind := "machine"
		if step.Monitor {
			kind = "spec"
		}
		frames = append(frames, frame(thread*framesPerThread+1, kind+" "+machine, m.Location()))
	}
	return frames
}

func frame(id int, name string, where source.Location) godap.StackFrame {
	f := godap.StackFrame{Id: id, Name: name, Line: where.Line, Column: max(where.Column, 1)}
	if where.File != "" {
		f.Source = &godap.Source{Name: filepath.Base(where.File), Path: where.File}
	} else {
		f.PresentationHint = "subtle"
	}
	return f
}

// describe says briefly what a step did.
func describe(step trace.Step) string {
	switch step.Kind {
	case "CreateStateMachine", "CreateMonitor":
		return "created"
	case "StateTransition", "MonitorStateTransition":
		if step.Entry {
			return "entry"
		}
		return "exit"
	case "DequeueEvent", "MonitorProcessEvent":
		return "on " + step.Event
	case "ReceiveEvent":
		return "receive case " + step.Event
	case "WaitMultipleEvents":
		return "receive, waiting for " + strings.Join(step.Waiting, " or ")
	case "SendEvent":
		return fmt.Sprintf("send %s to %s", step.Event, step.Target)
	case "RaiseEvent", "MonitorRaiseEvent":
		return "raise " + step.Event
	case "Announce":
		return "announce " + step.Event
	case "GotoState":
		return "goto " + step.EndState
	case "Print":
		return "print"
	case "AssertionFailure":
		return "assertion failed"
	}
	return step.Kind
}

// scopes returns the variables a frame shows: the machine's latest step,
// and the machine itself.
func (s *server) scopes(frameID int) []godap.Scope {
	machine := s.machine(frameID / framesPerThread)
	i := s.session.LastStep(machine)
	if i < 0 {
		return []godap.Scope{}
	}
	stepRef := s.store(s.stepVariables(i))
	machineRef := s.store(s.machineVariables(machine, i))
	return []godap.Scope{
		{Name: "Step", VariablesReference: stepRef, PresentationHint: "locals"},
		{Name: "Machine", VariablesReference: machineRef},
	}
}

// store keeps variables for a later variables request, and returns their
// reference.
func (s *server) store(vars []godap.Variable) int {
	ref := len(s.variables) + 1
	s.variables[ref] = vars
	return ref
}

func (s *server) stepVariables(i int) []godap.Variable {
	step := s.session.Steps[i]
	vars := []godap.Variable{
		{Name: "step", Value: fmt.Sprintf("%d of %d", i+1, len(s.session.Steps))},
		{Name: "kind", Value: step.Kind},
	}
	if step.Event != "" {
		vars = append(vars, godap.Variable{Name: "event", Value: step.Event})
	}
	if step.HasPayload() {
		vars = append(vars, s.jsonVariable("payload", step.Payload))
	}
	if step.Sender != "" {
		vars = append(vars, godap.Variable{Name: "sender", Value: step.Sender})
	}
	if step.Target != "" {
		vars = append(vars, godap.Variable{Name: "target", Value: step.Target})
	}
	if step.Error != "" {
		vars = append(vars, godap.Variable{Name: "error", Value: step.Error})
	}
	vars = append(vars, godap.Variable{Name: "log", Value: step.Log})
	return vars
}

func (s *server) machineVariables(machine string, last int) []godap.Variable {
	vars := []godap.Variable{
		{Name: "machine", Value: machine},
		{Name: "state", Value: s.session.State(machine)},
	}
	inbox := s.session.Inbox(machine)
	items := make([]godap.Variable, len(inbox))
	for j, m := range inbox {
		item := godap.Variable{Name: fmt.Sprint(j), Value: m.Event + " from " + m.Sender}
		if p := string(m.Payload); p != "" && p != "null" && p != `"null"` {
			item.Value += ": " + compact(m.Payload)
			item.VariablesReference = s.store([]godap.Variable{s.jsonVariable("payload", m.Payload)})
		}
		items[j] = item
	}
	vars = append(vars, godap.Variable{
		Name: "inbox", Value: fmt.Sprintf("%d waiting", len(inbox)),
		VariablesReference: s.store(items), IndexedVariables: len(items),
	})
	vars = append(vars, godap.Variable{Name: "latest step", Value: fmt.Sprintf("%d: %s", last+1, describe(s.session.Steps[last]))})
	return vars
}

// jsonVariable shows a JSON value, with objects and arrays expandable.
func (s *server) jsonVariable(name string, raw json.RawMessage) godap.Variable {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return godap.Variable{Name: name, Value: string(raw)}
	}
	return s.valueVariable(name, value)
}

func (s *server) valueVariable(name string, value any) godap.Variable {
	v := godap.Variable{Name: name}
	switch value := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(value))
		for k := range value {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		children := make([]godap.Variable, len(keys))
		for i, k := range keys {
			children[i] = s.valueVariable(k, value[k])
		}
		data, _ := json.Marshal(value)
		v.Value = compact(data)
		v.VariablesReference = s.store(children)
		v.NamedVariables = len(children)
	case []any:
		children := make([]godap.Variable, len(value))
		for i, e := range value {
			children[i] = s.valueVariable(fmt.Sprint(i), e)
		}
		data, _ := json.Marshal(value)
		v.Value = compact(data)
		v.VariablesReference = s.store(children)
		v.IndexedVariables = len(children)
	default:
		data, _ := json.Marshal(value)
		v.Value = string(data)
	}
	return v
}

// compact shortens JSON for showing on one line.
func compact(data []byte) string {
	text := string(data)
	if len(text) > 80 {
		text = text[:77] + "..."
	}
	return text
}
