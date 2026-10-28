// Package trace reads the JSON traces PChecker writes for the schedules it
// explores, such as the one that found a bug.
package trace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Step is one thing that happened in a schedule.
type Step struct {
	// Index is the step's position in the trace, from 0.
	Index int
	// Kind is PChecker's name for what happened, such as "SendEvent",
	// "DequeueEvent" or "StateTransition".
	Kind string
	// Machine is the machine or monitor that did it, as PChecker names it:
	// "Client(2)" for the second machine created, or a monitor's name.
	Machine string
	// Monitor reports whether Machine is a spec monitor.
	Monitor bool
	// State is the state Machine was in. For a GotoState, it's the state
	// being left, and EndState the state being entered.
	State, EndState string
	// Entry reports, for a StateTransition, whether the state is being
	// entered rather than exited.
	Entry bool
	// Event is the event sent, dequeued, received, raised or announced.
	Event string
	// Payload is the event's payload, or null, as compact JSON.
	Payload json.RawMessage
	// Sender and Target are the machines an event was sent from and to.
	Sender, Target string
	// Waiting is the events a blocking receive waits for.
	Waiting []string
	// Log is PChecker's description of the step, and Error, for an
	// AssertionFailure, its message.
	Log, Error string
	// Clock is the step's vector clock: how many steps of each machine
	// happened before it.
	Clock map[string]int
}

// MachineType returns the type of machine the step's machine is: "Client"
// for "Client(2)".
func (s Step) MachineType() string {
	return MachineType(s.Machine)
}

// MachineType returns the type of a machine named as PChecker names it.
func MachineType(machine string) string {
	if i := strings.LastIndexByte(machine, '('); i > 0 && strings.HasSuffix(machine, ")") {
		return machine[:i]
	}
	return machine
}

// HasPayload reports whether the step carries a payload.
func (s Step) HasPayload() bool {
	p := strings.TrimSpace(string(s.Payload))
	return p != "" && p != "null" && p != `"null"`
}

// raw is a trace entry as PChecker writes it.
type raw struct {
	Type    string `json:"type"`
	Details struct {
		Log        string          `json:"log"`
		ID         string          `json:"id"`
		Monitor    string          `json:"monitor"`
		Sender     string          `json:"sender"`
		Target     string          `json:"target"`
		State      string          `json:"state"`
		StartState string          `json:"startState"`
		EndState   string          `json:"endState"`
		IsEntry    bool            `json:"isEntry"`
		Event      string          `json:"event"`
		Payload    json.RawMessage `json:"payload"`
		EventTypes []string        `json:"eventTypes"`
		Error      string          `json:"error"`
		Clock      map[string]int  `json:"clock"`
	} `json:"details"`
}

// Load reads a trace file.
func Load(path string) ([]Step, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	steps, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return steps, nil
}

// Parse reads a trace. It's a list of steps, as PChecker writes for a
// schedule that found a bug, or with --verbose a list of schedules, of
// which it reads the last: the one that found a bug, if one did. Entries
// that describe the checker rather than the program, like the scheduling
// strategy, are left out.
func Parse(data []byte) ([]Step, error) {
	var entries []raw
	if err := json.Unmarshal(data, &entries); err != nil {
		var schedules [][]raw
		if json.Unmarshal(data, &schedules) != nil || len(schedules) == 0 {
			return nil, fmt.Errorf("not a PChecker trace: %w", err)
		}
		entries = schedules[len(schedules)-1]
	}
	return steps(entries), nil
}

func steps(entries []raw) []Step {
	var steps []Step
	last := "" // the machine that acted last
	for _, e := range entries {
		d := e.Details
		if e.Type == "StrategyDescription" {
			continue
		}
		// Payloads are compared, so whitespace mustn't differ.
		payload := d.Payload
		var compacted bytes.Buffer
		if json.Compact(&compacted, d.Payload) == nil {
			payload = compacted.Bytes()
		}
		s := Step{
			Index: len(steps), Kind: e.Type,
			State: d.State, EndState: d.EndState, Entry: d.IsEntry,
			Event: d.Event, Payload: payload,
			Sender: d.Sender, Target: d.Target, Waiting: d.EventTypes,
			Log: d.Log, Error: d.Error, Clock: d.Clock,
		}
		switch {
		case d.Monitor != "":
			// A monitor processing an event names its sender too, but the
			// step is the monitor's.
			s.Machine, s.Monitor = d.Monitor, true
		case d.ID != "":
			s.Machine = d.ID
		case d.Sender != "":
			s.Machine = d.Sender
		default:
			// Prints and assertion failures belong to the machine that
			// was running.
			s.Machine = last
		}
		if d.StartState != "" {
			s.State = d.StartState
		}
		if s.Log == "" {
			s.Log = d.Error
		}
		if !s.Monitor {
			// Monitors run within the step of the machine that sent the
			// event, so a print is that machine's.
			last = s.Machine
		}
		steps = append(steps, s)
	}
	return steps
}

// assertionLocation matches the location PChecker puts in an assertion's
// message: "Assertion Failed: PSrc/Client.p:46:13 message".
var assertionLocation = regexp.MustCompile(`(\S+\.p):(\d+):(\d+)`)

// AssertionLocation returns the file, relative to the project directory,
// and the 1-based line and column of a failed assertion.
func (s Step) AssertionLocation() (file string, line, column int, ok bool) {
	m := assertionLocation.FindStringSubmatch(s.Error)
	if m == nil {
		return "", 0, 0, false
	}
	line, _ = strconv.Atoi(m[2])
	column, _ = strconv.Atoi(m[3])
	return m[1], line, column, true
}
