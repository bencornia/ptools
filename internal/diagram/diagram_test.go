package diagram

import (
	"encoding/xml"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bencornia/ptools/internal/session"
)

func open(t *testing.T) *session.Session {
	t.Helper()
	s, closeSession, err := session.Open(filepath.Join("../../testdata/pingpong", "bug.trace.json"), session.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeSession)
	return s
}

func draw(t *testing.T, s *session.Session, opts Options) string {
	t.Helper()
	var b strings.Builder
	if err := SVG(&b, s, opts); err != nil {
		t.Fatal(err)
	}
	svg := b.String()
	// It's well-formed XML.
	dec := xml.NewDecoder(strings.NewReader(svg))
	for {
		if _, err := dec.Token(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("not XML: %v\n%s", err, svg)
		}
	}
	return svg
}

func TestSVG(t *testing.T) {
	svg := draw(t, open(t), Options{Title: "bug"})
	for _, want := range []string{
		"<title>bug</title>",
		">PongsCounted</text>", ">Client(2)</text>", ">Server(3)</text>",
		">ePing (Client(2), 1)</text>", // too long with the field names
		"<title>17: 'Client(2)' in state 'Pinging' sent event 'ePing with payload (&lt;client:Client(2), n:1, &gt;)' to 'Server(3)'.</title>",
		">eHelloBack</text>", // a null payload isn't shown
		">receive eHelloBack</text>",
		">Pinging</text>",
		">&quot;got pong 4&quot;</text>",
		">Assertion Failed: PSrc/PingPong.p:46:13 expected 3 pongs, got 4</text>",
		`class="fail-band"`,
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("no %s", want)
		}
	}
	// Every send was dequeued.
	if n := strings.Count(svg, `class="arrow send"`); n != 8 {
		t.Errorf("%d sends drawn, not 8", n)
	}
	if strings.Contains(svg, `class="arrow send open"`) {
		t.Error("a send is drawn as never dequeued")
	}
	if n := strings.Count(svg, `class="arrow create"`); n != 2 {
		t.Errorf("%d creations drawn, not 2", n)
	}
	if n := strings.Count(svg, `class="arrow observe"`); n != 3 {
		t.Errorf("%d monitor observations drawn, not 3", n)
	}
	// Steps that show as others, like exits, have no row.
	if strings.Contains(svg, ">exits state") || strings.Contains(svg, `class="num" x="48" y="0">14<`) {
		t.Error("an exit has a row")
	}
}

func TestSVGMachines(t *testing.T) {
	svg := draw(t, open(t), Options{Machines: []string{"Client"}, HideMonitors: true})
	if strings.Contains(svg, ">Server(3)</text>") || strings.Contains(svg, ">PongsCounted</text>") {
		t.Error("draws a machine left out")
	}
	// Sends to a machine left out say where they went.
	if !strings.Contains(svg, ">ePing (…) → Server(3)</text>") {
		t.Error("no stub for a send to the server")
	}
	// What arrives from it says what it was.
	if !strings.Contains(svg, `class="arrow send open"`) || !strings.Contains(svg, ">ePong</text>") {
		t.Error("no stub for an event from the server")
	}
}

func TestSVGSteps(t *testing.T) {
	s := open(t)
	// Step 20 sends the first ePong, which step 21 dequeues.
	svg := draw(t, s, Options{From: 15, To: 20})
	if !strings.Contains(svg, `class="arrow send open"`) {
		t.Error("an event dequeued after the last step drawn isn't open-ended")
	}
	if strings.Contains(svg, ">14</text>") || strings.Contains(svg, ">21</text>") {
		t.Error("draws steps out of range")
	}
	var b strings.Builder
	if err := SVG(&b, s, Options{From: 100}); err == nil {
		t.Error("drew no steps without an error")
	}
}

func TestSVGNeverDequeued(t *testing.T) {
	full := open(t)
	// The trace up to the server's first ePong, before the client dequeues it.
	s := session.New(full.Steps[:20], full.Source)
	svg := draw(t, s, Options{})
	if !strings.Contains(svg, "It was never dequeued.") || !strings.Contains(svg, `class="arrow send open"`) {
		t.Error("an event never dequeued isn't shown as such")
	}
}

func TestPayload(t *testing.T) {
	for _, c := range []struct{ raw, named, plain string }{
		{``, "", ""},
		{`null`, "", ""},
		{`"null"`, "", ""},
		{`4`, " (4)", " (4)"},
		{`"Client(2)"`, " (Client(2))", " (Client(2))"},
		{`{"client":"Client(2)","n":1}`, " (client = Client(2), n = 1)", " (Client(2), 1)"},
		{`{"b":2,"a":[1,2]}`, " (b = 2, a = [1,2])", " (2, [1,2])"},
		{`[1,2]`, " ([1,2])", " ([1,2])"},
	} {
		named, plain := payload([]byte(c.raw))
		if named != c.named || plain != c.plain {
			t.Errorf("payload(%s) = %q, %q; want %q, %q", c.raw, named, plain, c.named, c.plain)
		}
	}
}

func TestFit(t *testing.T) {
	if got := fit("eViewChangedFollower", 10*charW); got != "eViewChan…" {
		t.Errorf("fit = %q", got)
	}
	if got := fit("short", 10*charW); got != "short" {
		t.Errorf("fit = %q", got)
	}
}

func TestSVGRepeatsNames(t *testing.T) {
	full := open(t)
	if svg := draw(t, full, Options{}); strings.Contains(svg, "name repeat") {
		t.Error("repeats the names in a diagram of fewer than 36 rows")
	}
	// The trace twice over: 68 rows, so the names again before the 37th.
	steps := append(slices.Clone(full.Steps), full.Steps...)
	svg := draw(t, session.New(steps, full.Source), Options{})
	if n := strings.Count(svg, `class="name repeat`); n != 4 {
		t.Errorf("repeats %d names, not the 4 machines'", n)
	}
	if !strings.Contains(svg, `class="name repeat"><title>Main(1)</title>`) {
		t.Error("no repeated name for Main(1)")
	}
}
