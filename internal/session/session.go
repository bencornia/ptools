// Package session replays a trace step by step against its P source: where
// each step happened, which machines exist and what's waiting for them,
// and moving forward and back through the steps.
package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/bencornia/prdb/internal/replay"
	"github.com/bencornia/prdb/internal/source"
	"github.com/bencornia/prdb/internal/trace"
)

// Session is a trace being stepped through.
type Session struct {
	Steps  []trace.Step
	Source *source.Index
	// Where is each step's source location, and Scope the handler it
	// happened in. Either can be the zero Location if it isn't known.
	Where, Scope []source.Location
	// starts reports, for each step, whether it starts running a handler:
	// a state's entry or exit, or an event's handler.
	starts []bool
	// Pos is the current step.
	Pos int
	// breakpoints holds lines, by file.
	breakpoints map[string]map[int]bool
	// stepLines are the lines some step happens on, by file.
	stepLines map[string]map[int]bool
	// delivered is, for each step that sends an event, the step that
	// dequeued or received it, or -1 if none did.
	delivered []int
}

// New starts a session at the first step.
func New(steps []trace.Step, src *source.Index) *Session {
	s := &Session{Steps: steps, Source: src, breakpoints: map[string]map[int]bool{}, stepLines: map[string]map[int]bool{}}
	s.locate()
	s.deliver()
	for _, l := range s.Where {
		if l.File != "" {
			if s.stepLines[l.File] == nil {
				s.stepLines[l.File] = map[int]bool{}
			}
			s.stepLines[l.File][l.Line] = true
		}
	}
	return s
}

// machineState is what locate knows about a machine as it goes.
type machineState struct {
	state string
	// handler is the handler it's running, and inner the function it was
	// last found running inside the handler, where its next statement is
	// most likely to be.
	handler, inner source.Node
	hasHandler     bool
	hasInner       bool
}

// locate works out where each step happened.
func (s *Session) locate() {
	n := len(s.Steps)
	s.Where, s.Scope, s.starts = make([]source.Location, n), make([]source.Location, n), make([]bool, n)
	machines := map[string]*machineState{}
	for i, step := range s.Steps {
		ms := machines[step.Machine]
		if ms == nil {
			ms = &machineState{}
			machines[step.Machine] = ms
		}
		m := s.Source.Machines[step.MachineType()]

		switch step.Kind {
		case "CreateStateMachine", "CreateMonitor":
			if m != nil {
				s.Where[i] = m.Location()
			}

		case "StateTransition", "MonitorStateTransition":
			ms.state = step.State
			ms.hasHandler, ms.hasInner = false, false
			if m == nil {
				break
			}
			state, ok := m.States[step.State]
			if !ok {
				break
			}
			s.Where[i] = state.Location()
			if handler, ok := s.Source.StateHandler(m, state, step.Entry); ok {
				ms.handler, ms.hasHandler = handler, true
				s.Where[i] = handler.Location()
				s.starts[i] = true
			}

		case "DequeueEvent", "MonitorProcessEvent":
			if step.State != "" {
				ms.state = step.State
			}
			ms.hasHandler, ms.hasInner = false, false
			if m == nil {
				break
			}
			state, ok := m.States[ms.state]
			if !ok {
				break
			}
			s.Where[i] = state.Location()
			if handler, ok := s.Source.EventHandler(m, state, step.Event); ok {
				ms.handler, ms.hasHandler = handler, true
				s.Where[i] = handler.Location()
				s.starts[i] = true
			}

		case "SendEvent":
			s.Where[i] = s.send(m, ms, sourceEvent(step.Event), step.Payload)
		case "RaiseEvent", "MonitorRaiseEvent":
			if step.Event == "GotoStateEvent" {
				// How PChecker reports a goto in a monitor.
				s.Where[i] = s.statement(m, ms, "goto_statement", "")
			} else {
				s.Where[i] = s.statement(m, ms, "raise_statement", sourceEvent(step.Event))
			}
		case "Announce":
			s.Where[i] = s.statement(m, ms, "announce_statement", step.Event)
		case "GotoState":
			s.Where[i] = s.statement(m, ms, "goto_statement", step.EndState)
		case "Print":
			s.Where[i] = s.statement(m, ms, "print_statement", step.Log)
		case "WaitMultipleEvents":
			waitingFor := ""
			for _, e := range step.Waiting {
				if e != "PHalt" {
					waitingFor = e
					break
				}
			}
			s.Where[i] = s.statement(m, ms, "receive_statement", waitingFor)
		case "ReceiveEvent":
			s.Where[i] = s.statement(m, ms, "recv_case", step.Event)

		case "AssertionFailure":
			if file, line, column, ok := step.AssertionLocation(); ok {
				path := filepath.Join(s.Source.Root, filepath.FromSlash(file))
				s.Where[i] = source.Location{File: path, Line: line, Column: column, EndLine: line}
			} else {
				s.Where[i] = s.statement(m, ms, "assert_statement", "")
			}
		}

		if ms.hasHandler {
			s.Scope[i] = ms.handler.Location()
		}
		if s.Where[i].File == "" {
			// Somewhere in the handler, or the state, or the machine, at
			// least: steps like halting have no statement behind them.
			switch state, ok := m.StateNamed(ms.state); {
			case ms.hasHandler:
				s.Where[i] = ms.handler.Location()
			case ok:
				s.Where[i] = state.Location()
			case m != nil:
				s.Where[i] = m.Location()
			}
		}
	}
}

// sourceEvent returns how an event the trace names is written in P: the
// halt event is PHalt in traces.
func sourceEvent(event string) string {
	if event == "PHalt" {
		return "halt"
	}
	return event
}

// statement finds where a machine did something: in the function it was
// last found in, then in its handler, then anywhere in the machine, since
// handlers call functions.
func (s *Session) statement(m *source.Machine, ms *machineState, kind, name string) source.Location {
	return s.find(m, ms, kind, name, nil)
}

// send finds a send statement, as statement does, and of the sends of the
// event in the first scope that has any, the one whose payload agrees best
// with what was sent: a handler may send the same event in more than one
// place, with different values.
func (s *Session) send(m *source.Machine, ms *machineState, event string, payload json.RawMessage) source.Location {
	return s.find(m, ms, "send_statement", event, payload)
}

func (s *Session) find(m *source.Machine, ms *machineState, kind, name string, payload json.RawMessage) source.Location {
	statement := func(scope source.Node, name string) (source.Node, bool) {
		if payload == nil {
			return s.Source.Statement(scope, kind, name)
		}
		return s.bestSend(scope, name, payload)
	}
	if m == nil {
		return source.Location{}
	}
	scopes := []source.Node{}
	if ms.hasInner {
		scopes = append(scopes, ms.inner)
	}
	if ms.hasHandler {
		scopes = append(scopes, ms.handler)
	}
	for _, scope := range scopes {
		if n, ok := statement(scope, name); ok {
			return n.Location()
		}
	}
	// The machine's functions, then the top-level ones, which handlers
	// also call, as in StartTimer(timer).
	functions := append(m.FunctionsInOrder(), s.Source.FunctionsInOrder()...)
	for _, fn := range functions {
		if n, ok := statement(fn, name); ok {
			ms.inner, ms.hasInner = fn, true
			return n.Location()
		}
	}
	if n, ok := statement(m.Node, name); ok {
		return n.Location()
	}
	// A helper that sends whatever event it's given, as in
	// send target, e, payload.
	if kind == "send_statement" {
		for _, scope := range append(scopes, functions...) {
			if n, ok := statement(scope, source.AnyEvent); ok {
				ms.inner, ms.hasInner = scope, true
				return n.Location()
			}
		}
	}
	return source.Location{}
}

// bestSend returns, of the sends of event in scope, the one whose payload
// agrees best with payload, or the first if none agrees.
func (s *Session) bestSend(scope source.Node, event string, payload json.RawMessage) (source.Node, bool) {
	sends := s.Source.Statements(scope, "send_statement", event)
	if len(sends) == 0 {
		return source.Node{}, false
	}
	best, most := sends[0], -1
	for _, send := range sends {
		if agrees, confirmed := s.Source.PayloadAgrees(send, payload); agrees && confirmed > most {
			best, most = send, confirmed
		}
	}
	return best, true
}

// Current returns the current step.
func (s *Session) Current() trace.Step {
	return s.Steps[s.Pos]
}

// AtEnd reports whether the current step is the last.
func (s *Session) AtEnd() bool {
	return s.Pos == len(s.Steps)-1
}

// StepIn moves to the next step of any machine, and reports whether there
// was one.
func (s *Session) StepIn() bool {
	if s.AtEnd() {
		return false
	}
	s.Pos++
	return true
}

// Next moves to the next step of machine, or to the end if it has none,
// stopping early at a failed assertion, and reports whether it moved.
func (s *Session) Next(machine string) bool {
	return s.forward(func(i int) bool { return s.Steps[i].Machine == machine })
}

// StepOut moves to the next step that starts a handler of machine, which
// is past the end of the handler it's running.
func (s *Session) StepOut(machine string) bool {
	return s.forward(func(i int) bool { return s.Steps[i].Machine == machine && s.starts[i] })
}

// Continue moves forward to the next breakpoint, or to the end.
func (s *Session) Continue() bool {
	return s.forward(s.HitsBreakpoint)
}

// forward moves to the next step that stop accepts, a failed assertion, or
// the last step, and reports whether it moved.
func (s *Session) forward(stop func(int) bool) bool {
	if s.AtEnd() {
		return false
	}
	for s.Pos++; !s.AtEnd(); s.Pos++ {
		if stop(s.Pos) || s.Steps[s.Pos].Kind == "AssertionFailure" {
			break
		}
	}
	return true
}

// StepBack moves to the previous step of machine, or of any machine if
// machine is "", or to the first step, and reports whether it moved.
func (s *Session) StepBack(machine string) bool {
	return s.backward(func(i int) bool { return machine == "" || s.Steps[i].Machine == machine })
}

// ReverseContinue moves back to the previous breakpoint, or to the first
// step.
func (s *Session) ReverseContinue() bool {
	return s.backward(s.HitsBreakpoint)
}

func (s *Session) backward(stop func(int) bool) bool {
	if s.Pos == 0 {
		return false
	}
	for s.Pos--; s.Pos > 0; s.Pos-- {
		if stop(s.Pos) {
			break
		}
	}
	return true
}

// SetBreakpoints replaces the breakpoints in file, and reports for each
// line whether a step can stop there.
func (s *Session) SetBreakpoints(file string, lines []int) []bool {
	s.breakpoints[file] = map[int]bool{}
	verified := make([]bool, len(lines))
	for i, line := range lines {
		s.breakpoints[file][line] = true
		for j := range s.Steps {
			if s.hits(j, file, line) {
				verified[i] = true
				break
			}
		}
	}
	return verified
}

// HitsBreakpoint reports whether step i stops at a breakpoint: one on the
// step's line, or, for a step that starts a handler, one in the handler on
// a line where nothing else stops.
func (s *Session) HitsBreakpoint(i int) bool {
	for file, lines := range s.breakpoints {
		for line := range lines {
			if s.hits(i, file, line) {
				return true
			}
		}
	}
	return false
}

func (s *Session) hits(i int, file string, line int) bool {
	w := s.Where[i]
	if w.File == file && w.Line == line {
		return true
	}
	return s.starts[i] && w.Contains(file, line) && !s.stepLines[file][line]
}

// Machines returns the machines that exist at the current step, in the
// order they were created.
func (s *Session) Machines() []string {
	var machines []string
	seen := map[string]bool{}
	for _, step := range s.Steps[:s.Pos+1] {
		if step.Machine != "" && !seen[step.Machine] {
			seen[step.Machine] = true
			machines = append(machines, step.Machine)
		}
	}
	return machines
}

// AllMachines returns every machine in the trace, in the order they first
// act.
func (s *Session) AllMachines() []string {
	var machines []string
	seen := map[string]bool{}
	for _, step := range s.Steps {
		if step.Machine != "" && !seen[step.Machine] {
			seen[step.Machine] = true
			machines = append(machines, step.Machine)
		}
	}
	return machines
}

// LastStep returns the index of machine's last step up to the current one,
// or -1 if it hasn't acted yet.
func (s *Session) LastStep(machine string) int {
	for i := s.Pos; i >= 0; i-- {
		if s.Steps[i].Machine == machine {
			return i
		}
	}
	return -1
}

// State returns the state machine is in at the current step: before it
// enters one, its start state.
func (s *Session) State(machine string) string {
	start := ""
	for i := s.Pos; i >= 0; i-- {
		step := s.Steps[i]
		if step.Machine == machine && start == "" {
			if m := s.Source.Machines[step.MachineType()]; m != nil {
				start = m.Start
			}
		}
		if step.Machine != machine {
			continue
		}
		switch step.Kind {
		case "StateTransition", "MonitorStateTransition":
			return step.State
		case "GotoState":
			return step.EndState
		}
		if step.State != "" {
			return step.State
		}
	}
	return start
}

// Message is an event sent to a machine.
type Message struct {
	Event   string
	Payload []byte
	Sender  string
	// Step is the index of the step that sent it.
	Step int
}

// Inbox returns the events sent to machine that it hasn't dequeued or
// received by the current step, oldest first.
func (s *Session) Inbox(machine string) []Message {
	var inbox []Message
	for i, step := range s.Steps[:s.Pos+1] {
		if step.Kind == "SendEvent" && step.Target == machine && (s.delivered[i] < 0 || s.delivered[i] > s.Pos) {
			inbox = append(inbox, Message{Event: step.Event, Payload: step.Payload, Sender: step.Sender, Step: i})
		}
	}
	return inbox
}

// Delivered returns the step that dequeued or received the event step i
// sent, or -1 if none did, or step i doesn't send one.
func (s *Session) Delivered(i int) int {
	return s.delivered[i]
}

// deliver matches each dequeue and receive to the send it took from the
// machine's inbox: the first waiting event like it, since events can be
// taken out of order by a receive or a deferral.
func (s *Session) deliver() {
	s.delivered = make([]int, len(s.Steps))
	waiting := map[string][]int{} // sends not yet delivered, by target
	for i, step := range s.Steps {
		s.delivered[i] = -1
		switch step.Kind {
		case "SendEvent":
			waiting[step.Target] = append(waiting[step.Target], i)
		case "DequeueEvent", "ReceiveEvent":
			inbox := waiting[step.Machine]
			for j, send := range inbox {
				sent := s.Steps[send]
				if sent.Event == step.Event && (bytes.Equal(sent.Payload, step.Payload) || !step.HasPayload()) {
					s.delivered[send] = i
					waiting[step.Machine] = append(inbox[:j], inbox[j+1:]...)
					break
				}
			}
		}
	}
}

// Options are how Open finds and makes what it loads.
type Options struct {
	// Project is the project's directory, or "" to find it: the nearest
	// directory, from the trace's up, with a .pproj file.
	Project string
	// Compiler runs the P tool, to replay a schedule; "p" by default.
	Compiler []string
	// Progress, if not nil, hears what replaying a schedule or checking a
	// test case is doing.
	Progress io.Writer
	// TestCase, if set, is a test case to check until PChecker finds a
	// bug, whose trace Open loads in place of path's, or if none does, one
	// run of it. A parameterised test can be named for all its cases. The
	// project is then found from the current directory, unless Project is
	// set.
	TestCase string
	// Schedules is how many schedules to explore for a bug in TestCase:
	// 1000 by default.
	Schedules int
	// Schedule picks which of a --verbose trace's schedules to load,
	// counting from 1. By default it's the last, the one that found a bug
	// if one did.
	Schedule int
}

// Open loads a trace, or a .schedule file replayed into one, or the bug
// PChecker finds in opts.TestCase, and the source of the project it's
// from. close frees the parsed source.
func Open(path string, opts Options) (s *Session, close func(), err error) {
	project := opts.Project
	if project == "" {
		from := filepath.Dir(path)
		if opts.TestCase != "" {
			from = "."
		}
		project = FindProject(from)
	}
	if project, err = filepath.Abs(project); err != nil {
		return nil, nil, err
	}
	tracePath := path
	switch {
	case opts.TestCase != "":
		schedules := opts.Schedules
		if schedules <= 0 {
			schedules = 1000
		}
		found, _, clean, err := replay.TestCase(opts.TestCase, project, schedules, opts.Compiler, opts.Progress)
		if err != nil {
			return nil, nil, err
		}
		defer clean()
		tracePath, path = found, found
	case strings.HasSuffix(path, ".schedule"):
		replayed, clean, err := replay.Schedule(path, project, opts.Compiler, opts.Progress)
		if err != nil {
			return nil, nil, err
		}
		defer clean()
		tracePath = replayed
	}
	schedules, err := trace.LoadSchedules(tracePath)
	if err != nil {
		return nil, nil, err
	}
	n := len(schedules)
	if opts.Schedule < 0 || opts.Schedule > n {
		return nil, nil, fmt.Errorf("%s has %d schedules; there's no schedule %d", path, n, opts.Schedule)
	}
	pick := n
	if opts.Schedule > 0 {
		pick = opts.Schedule
	}
	if n > 1 && opts.Progress != nil {
		fmt.Fprintf(opts.Progress, "%s has %d schedules; this is schedule %d\n", filepath.Base(path), n, pick)
	}
	steps := schedules[pick-1]
	if len(steps) == 0 {
		return nil, nil, fmt.Errorf("%s has no steps", path)
	}
	src, err := source.Load(project)
	if err != nil {
		return nil, nil, err
	}
	return New(steps, src), src.Close, nil
}

// FindProject returns the nearest directory, from dir up, that has a .pproj
// file in it, or dir if none does.
func FindProject(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	for d := abs; ; d = filepath.Dir(d) {
		if matches, _ := filepath.Glob(filepath.Join(d, "*.pproj")); len(matches) > 0 {
			return d
		}
		if filepath.Dir(d) == d {
			return abs
		}
	}
}
