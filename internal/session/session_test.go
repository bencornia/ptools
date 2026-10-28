package session

import (
	"fmt"
	"path/filepath"
	"testing"
)

const fixture = "../../testdata/pingpong"

func open(t *testing.T) *Session {
	t.Helper()
	s, closeSession, err := Open(filepath.Join(fixture, "bug.trace.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeSession)
	return s
}

// where renders step i's location as "file:line".
func where(s *Session, i int) string {
	w := s.Where[i]
	if w.File == "" {
		return "?"
	}
	rel, _ := filepath.Rel(s.Source.Root, w.File)
	return fmt.Sprintf("%s:%d", filepath.ToSlash(rel), w.Line)
}

func TestLocations(t *testing.T) {
	s := open(t)
	if want, _ := filepath.Abs(fixture); s.Source.Root != want {
		t.Errorf("project %s, want %s", s.Source.Root, want)
	}
	for i, want := range map[int]string{
		0:  "PSpec/Spec.p:2",     // the monitor's declaration
		5:  "PSrc/PingPong.p:31", // Client's Init entry handler
		8:  "PSrc/PingPong.p:54", // a send in Hello(), which the entry handler calls
		9:  "PSrc/PingPong.p:55", // the receive in Hello()
		12: "PSrc/PingPong.p:56", // its case
		13: "PSrc/PingPong.p:34", // goto Pinging, back in the entry handler
		14: "PSrc/PingPong.p:30", // leaving Init, which has no exit handler
		17: "PSrc/PingPong.p:16", // the server's on ePing handler
		18: "PSpec/Spec.p:6",     // the monitor's on ePong handler
		19: "PSrc/PingPong.p:21", // the server's send of ePong
		21: "PSrc/PingPong.p:45", // the print
		22: "PSrc/PingPong.p:48", // goto Pinging, in the on ePong handler
		40: "PSrc/PingPong.p:46", // the failed assertion
	} {
		if got := where(s, i); got != want {
			t.Errorf("step %d (%s): at %s, want %s", i, s.Steps[i].Log, got, want)
		}
	}
	for i := range s.Steps {
		if s.Where[i].File == "" {
			t.Errorf("step %d (%s) has no location", i, s.Steps[i].Log)
		}
	}
}

func TestStepping(t *testing.T) {
	s := open(t)
	if s.StepBack("") {
		t.Error("stepped back from the first step")
	}
	s.StepIn()
	if s.Pos != 1 {
		t.Errorf("step in went to %d", s.Pos)
	}

	// Next follows one machine.
	s.Pos = 10
	s.Next("Server(3)")
	if s.Pos != 11 || s.Current().Machine != "Server(3)" {
		t.Errorf("next went to %d", s.Pos)
	}
	s.Next("Server(3)")
	if s.Pos != 17 {
		t.Errorf("next went to %d, want the server's next step, 17", s.Pos)
	}

	// Step out finishes the handler: the client's next handler starts.
	s.Pos = 15
	s.StepOut("Client(2)")
	if s.Pos != 20 || s.Current().Kind != "DequeueEvent" {
		t.Errorf("step out went to %d", s.Pos)
	}

	s.StepBack("Server(3)")
	if s.Pos != 19 {
		t.Errorf("step back went to %d", s.Pos)
	}

	// Moving forward stops at the failed assertion.
	s.Pos = 30
	s.Next("Server(3)")
	s.Next("Server(3)")
	s.Next("Server(3)")
	if s.Current().Kind != "AssertionFailure" || !s.AtEnd() {
		t.Errorf("next went to %d", s.Pos)
	}
	if s.Next("Server(3)") || s.StepIn() || s.Continue() {
		t.Error("moved past the end")
	}
}

func TestBreakpoints(t *testing.T) {
	s := open(t)
	file := filepath.Join(s.Source.Root, "PSrc", "PingPong.p")

	// The server's send of ePong, and a line in its handler where no step
	// happens, which stops where the handler starts. A blank line can't
	// stop.
	verified := s.SetBreakpoints(file, []int{21, 17, 25})
	if fmt.Sprint(verified) != "[true true false]" {
		t.Errorf("verified %v", verified)
	}
	var stops []int
	for s.Continue() && !s.AtEnd() {
		stops = append(stops, s.Pos)
	}
	if want := "[17 19 26 28 35 37]"; fmt.Sprint(stops) != want {
		t.Errorf("stopped at %v, want %s", stops, want)
	}
	s.ReverseContinue()
	if s.Pos != 37 {
		t.Errorf("reverse continue went to %d", s.Pos)
	}

	s.SetBreakpoints(file, nil)
	s.Pos = 0
	s.Continue()
	if !s.AtEnd() {
		t.Errorf("with no breakpoints, continue stopped at %d", s.Pos)
	}
}

func TestMachines(t *testing.T) {
	s := open(t)
	s.Pos = 16 // the client has just sent ePing
	if got := fmt.Sprint(s.Machines()); got != "[PongsCounted Main(1) Client(2) Server(3)]" {
		t.Errorf("machines %s", got)
	}
	if got := s.State("Client(2)"); got != "Pinging" {
		t.Errorf("client state %q", got)
	}
	s.Pos = 0 // the monitor was created, and hasn't entered a state
	if got := s.State("PongsCounted"); got != "Watching" {
		t.Errorf("monitor's state before it starts %q", got)
	}
	s.Pos = 16
	inbox := s.Inbox("Server(3)")
	if len(inbox) != 1 || inbox[0].Event != "ePing" || inbox[0].Sender != "Client(2)" {
		t.Errorf("server inbox %+v", inbox)
	}
	s.Pos = 17 // the server dequeued it
	if inbox := s.Inbox("Server(3)"); len(inbox) != 0 {
		t.Errorf("server inbox after dequeuing %+v", inbox)
	}
	if got := s.LastStep("Server(3)"); got != 17 {
		t.Errorf("server's last step %d", got)
	}
	s.Pos = 3
	if got := s.LastStep("Server(3)"); got != -1 {
		t.Errorf("server acted before it existed: %d", got)
	}
}

func TestFindProject(t *testing.T) {
	want, _ := filepath.Abs(fixture)
	if got := FindProject(filepath.Join(fixture, "PSrc")); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
