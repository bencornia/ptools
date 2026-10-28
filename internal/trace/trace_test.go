package trace

import "testing"

func TestLoad(t *testing.T) {
	steps, err := Load("../../testdata/pingpong/bug.trace.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 41 {
		t.Fatalf("got %d steps, want 41 (without the strategy description)", len(steps))
	}
	for i, s := range steps {
		if s.Index != i {
			t.Errorf("step %d has index %d", i, s.Index)
		}
	}

	send := steps[16]
	if send.Kind != "SendEvent" || send.Machine != "Client(2)" || send.Target != "Server(3)" || send.Event != "ePing" ||
		send.State != "Pinging" || string(send.Payload) != `{"client":"Client(2)","n":1}` {
		t.Errorf("send: %+v", send)
	}
	if monitor := steps[18]; !monitor.Monitor || monitor.Machine != "PongsCounted" || monitor.Sender != "Server(3)" {
		t.Errorf("a monitor's step should be the monitor's: %+v", monitor)
	}
	if goTo := steps[13]; goTo.State != "Init" || goTo.EndState != "Pinging" {
		t.Errorf("goto: %+v", goTo)
	}
	// Prints and failed assertions belong to the machine that was running,
	// not a monitor that ran during its step.
	if p := steps[21]; p.Kind != "Print" || p.Machine != "Client(2)" || p.Log != "got pong 1" {
		t.Errorf("print: %+v", p)
	}
	failure := steps[40]
	if failure.Kind != "AssertionFailure" || failure.Machine != "Client(2)" {
		t.Errorf("assertion: %+v", failure)
	}
	file, line, column, ok := failure.AssertionLocation()
	if !ok || file != "PSrc/PingPong.p" || line != 46 || column != 13 {
		t.Errorf("assertion location %s:%d:%d %v", file, line, column, ok)
	}
	if steps[2].MachineType() != "Main" || steps[0].MachineType() != "PongsCounted" {
		t.Errorf("machine types %q %q", steps[2].MachineType(), steps[0].MachineType())
	}
}

func TestParseRejectsOtherJSON(t *testing.T) {
	if _, err := Parse([]byte(`{"not": "a trace"}`)); err == nil {
		t.Error("parsed an object as a trace")
	}
}

func TestParseVerboseTrace(t *testing.T) {
	// --verbose writes every schedule; the last is the one to replay.
	verbose := `[
		[{"type": "CreateStateMachine", "details": {"id": "A(1)", "log": "first schedule"}}],
		[{"type": "CreateStateMachine", "details": {"id": "B(1)", "log": "second schedule"}},
		 {"type": "StrategyDescription", "details": {"strategy": "random"}}]
	]`
	steps, err := Parse([]byte(verbose))
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Machine != "B(1)" {
		t.Errorf("got %+v", steps)
	}
}

func TestParseSchedules(t *testing.T) {
	schedules, err := ParseSchedules([]byte(`[
		[{"type": "CreateStateMachine", "details": {"id": "A(1)"}}],
		[{"type": "CreateStateMachine", "details": {"id": "B(1)"}},
		 {"type": "CreateStateMachine", "details": {"id": "C(2)"}}]
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(schedules) != 2 || len(schedules[0]) != 1 || len(schedules[1]) != 2 || schedules[1][1].Machine != "C(2)" {
		t.Errorf("got %+v", schedules)
	}
	// A bug's trace is one schedule.
	schedules, err = ParseSchedules([]byte(`[{"type": "CreateStateMachine", "details": {"id": "A(1)"}}]`))
	if err != nil || len(schedules) != 1 || schedules[0][0].Machine != "A(1)" {
		t.Errorf("got %+v, %v", schedules, err)
	}
}
