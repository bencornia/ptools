// Package diagram draws a trace as a space-time diagram, in SVG: a column
// for each machine, with time running down, a row for each step, and an
// arrow for each event from the step that sent it to the step that
// dequeued it. An event that waited in an inbox while other things
// happened slants down across those rows.
package diagram

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/bencornia/ptools/internal/session"
	"github.com/bencornia/ptools/internal/trace"
)

// Options choose what to draw.
type Options struct {
	// Machines are the machines to draw, by name ("Client(2)") or type
	// ("Client"). Empty draws them all.
	Machines []string
	// From and To are the first and last steps to draw, counting from 1
	// as the debugger does. Zero leaves that end open.
	From, To int
	// HideMonitors leaves out spec monitors.
	HideMonitors bool
	// Title names the diagram, for tooltips and screen readers.
	Title string
}

const (
	gutter  = 56  // left margin, for step numbers
	colW    = 180 // width of a machine's column
	rowH    = 24  // height of a step's row
	headH   = 44  // height of the machines' names, top and bottom
	charW   = 7.2 // width of a character of 12px monospace text
	margin  = 16
	labelGo = 8 // gap between a lifeline and a label beside it
	// every is how many rows go between the machines' names, repeated
	// down a long diagram so its columns can be told apart anywhere.
	every = 36
	// nameH is the height of a row of repeated names: room for the
	// boxes, clear of the steps either side.
	nameH = 40
)

// SVG writes the diagram of s's steps.
func SVG(w io.Writer, s *session.Session, opts Options) error {
	d := newDiagram(s, opts)
	if len(d.rows) == 0 {
		return fmt.Errorf("no steps to draw")
	}
	d.draw()
	_, err := io.WriteString(w, d.out.String())
	return err
}

type diagram struct {
	s    *session.Session
	opts Options
	// machines are the machines drawn, in the order they first act, then
	// the monitors, and col each one's column.
	machines []string
	col      map[string]int
	monitor  map[string]bool
	// steps are the steps drawn, and row each one's row.
	steps []int
	rows  map[int]int
	// right is the x of the right end of the furthest text.
	right float64
	// life is the top and end of each machine's lifeline.
	life map[string][2]float64
	out  strings.Builder
}

func newDiagram(s *session.Session, opts Options) *diagram {
	d := &diagram{s: s, opts: opts, col: map[string]int{}, monitor: map[string]bool{}, rows: map[int]int{}}
	for _, step := range s.Steps {
		if step.Monitor {
			d.monitor[step.Machine] = true
		}
	}
	// Machines first, then the monitors that watch them.
	all := s.AllMachines()
	slices.SortStableFunc(all, func(a, b string) int {
		switch {
		case d.monitor[a] == d.monitor[b]:
			return 0
		case d.monitor[a]:
			return 1
		}
		return -1
	})
	for _, m := range all {
		if d.shows(m) {
			d.col[m] = len(d.machines)
			d.machines = append(d.machines, m)
		}
	}
	for i, step := range s.Steps {
		n := i + 1
		if (opts.From > 0 && n < opts.From) || (opts.To > 0 && n > opts.To) {
			continue
		}
		if d.draws(step) {
			d.rows[i] = len(d.steps)
			d.steps = append(d.steps, i)
		}
	}
	return d
}

// shows reports whether machine m is drawn.
func (d *diagram) shows(m string) bool {
	if d.monitor[m] && d.opts.HideMonitors {
		return false
	}
	if len(d.opts.Machines) == 0 {
		return true
	}
	return slices.Contains(d.opts.Machines, m) || slices.Contains(d.opts.Machines, trace.MachineType(m))
}

// draws reports whether a step gets a row: it shows something on a
// machine drawn. Exiting a state, and the goto that does it, show as
// entering the next one.
func (d *diagram) draws(step trace.Step) bool {
	if _, ok := d.col[step.Machine]; !ok {
		return false
	}
	switch step.Kind {
	case "StateTransition", "MonitorStateTransition":
		return step.Entry
	case "MonitorRaiseEvent":
		// How PChecker reports a goto in a monitor.
		return step.Event != "GotoStateEvent"
	case "CreateStateMachine", "CreateMonitor", "SendEvent", "DequeueEvent", "ReceiveEvent",
		"WaitMultipleEvents", "RaiseEvent", "MonitorProcessEvent", "Print", "AssertionFailure",
		"Halt", "ExceptionThrown", "PopStateUnhandledEvent":
		return true
	}
	return false
}

func (d *diagram) x(machine string) float64 {
	return gutter + float64(d.col[machine])*colW + colW/2
}

// y is the middle of a step's row, below the rows of repeated names before
// it.
func (d *diagram) y(row int) float64 {
	return headH + margin + float64(row)*rowH + float64(row/every)*nameH + rowH/2
}

// bottom is the y of the end of the lifelines.
func (d *diagram) bottom() float64 {
	return d.y(len(d.steps)-1) + rowH
}

func (d *diagram) printf(format string, args ...any) {
	fmt.Fprintf(&d.out, format, args...)
}

func (d *diagram) draw() {
	height := d.bottom() + margin + headH
	d.lifelines()
	d.names(headH/2, "")
	d.names(height-headH/2, "")
	for row, i := range d.steps {
		d.step(row, i)
	}
	// On top of the arrows, which pass behind them.
	for row := every; row < len(d.steps); row += every {
		d.names(d.y(row)-rowH/2-nameH/2, "repeat")
	}
	body := d.out.String()
	width := max(gutter+float64(len(d.machines))*colW, d.right) + margin

	d.out.Reset()
	d.printf(`<svg xmlns="http://www.w3.org/2000/svg" width="%g" height="%g" viewBox="0 0 %g %g" role="img">`+"\n", width, height, width, height)
	if d.opts.Title != "" {
		d.printf("<title>%s</title>\n", esc(d.opts.Title))
	}
	d.printf("%s", style)
	d.printf(`<rect class="bg" width="100%%" height="100%%"/>` + "\n")
	// Every other row is shaded, to follow a row across.
	for row, i := range d.steps {
		if d.s.Steps[i].Kind == "AssertionFailure" {
			d.printf(`<rect class="fail-band" x="0" y="%g" width="%g" height="%d"/>`+"\n", d.y(row)-rowH/2, width, rowH)
		} else if row%2 == 1 {
			d.printf(`<rect class="band" x="0" y="%g" width="%g" height="%d"/>`+"\n", d.y(row)-rowH/2, width, rowH)
		}
		d.printf(`<text class="num" x="%d" y="%g">%d</text>`+"\n", gutter-labelGo, d.y(row)+4, i+1)
	}
	// Each step's row, marked with its number, for a viewer to highlight
	// or click.
	for row, i := range d.steps {
		d.printf(`<rect class="row" data-step="%d" x="0" y="%g" width="%g" height="%d"/>`+"\n", i+1, d.y(row)-rowH/2, width, rowH)
	}
	d.out.WriteString(body)
	d.printf("</svg>\n")
}

// lifelines draws each machine's line, from its creation, or the top, to
// its halting, or the bottom.
func (d *diagram) lifelines() {
	d.life = map[string][2]float64{}
	for _, m := range d.machines {
		top, end := float64(headH), d.bottom()+margin
		created, halted := false, false
		for row, i := range d.steps {
			step := d.s.Steps[i]
			if step.Machine != m {
				continue
			}
			switch {
			case (step.Kind == "CreateStateMachine" || step.Kind == "CreateMonitor") && !created:
				top, created = d.y(row), true
			case step.Kind == "Halt" && !halted:
				end, halted = d.y(row), true
			}
		}
		class := "life"
		if d.monitor[m] {
			class = "life mon"
		}
		d.life[m] = [2]float64{top, end}
		d.printf(`<line class="%s" x1="%g" y1="%g" x2="%g" y2="%g"/>`+"\n", class, d.x(m), top, d.x(m), end)
	}
}

// names draws the machines' names, centered on y. Repeated down the
// diagram, they're only on the lifelines there.
func (d *diagram) names(y float64, repeat string) {
	for _, m := range d.machines {
		if life := d.life[m]; repeat != "" && (y < life[0] || y > life[1]) {
			continue
		}
		name := fit(m, colW-24)
		w := float64(len(name))*charW + 16
		class := strings.TrimSpace("name " + repeat)
		if d.monitor[m] {
			class += " mon"
		}
		d.printf(`<g class="%s"><title>%s</title><rect x="%g" y="%g" width="%g" height="24" rx="4"/><text x="%g" y="%g">%s</text></g>`+"\n",
			class, esc(m), d.x(m)-w/2, y-12, w, d.x(m), y+4, esc(name))
	}
}

func (d *diagram) step(row, i int) {
	step := d.s.Steps[i]
	x, y := d.x(step.Machine), d.y(row)
	title := step.Log
	if step.Kind == "SendEvent" && d.s.Delivered(i) < 0 {
		title += " It was never dequeued."
	}
	d.printf(`<g class="step" data-step="%d"><title>%d: %s</title>`, i+1, i+1, esc(title))
	switch step.Kind {
	case "CreateStateMachine", "CreateMonitor":
		if _, ok := d.col[step.Creator]; ok {
			d.arrow("create", d.x(step.Creator), y, x, y)
			d.label("muted", d.x(step.Creator), x, y, "new")
		}
		d.dot("", x, y)
	case "StateTransition", "MonitorStateTransition":
		d.pill(x, y, step.State)
	case "SendEvent":
		d.send(row, i)
	case "DequeueEvent", "ReceiveEvent":
		d.dequeue(i, x, y)
	case "MonitorProcessEvent":
		text := d.event(step, colW-labelGo*2)
		if _, ok := d.col[step.Sender]; ok && step.Sender != step.Machine {
			d.arrow("observe", d.x(step.Sender), y, x, y)
			d.label("mon", d.x(step.Sender), x, y, text)
		} else {
			d.side("mon", x, y, text, colW-labelGo*2)
		}
		d.dot("mon", x, y)
	case "WaitMultipleEvents":
		d.dot("", x, y)
		// Every receive takes PHalt; it says nothing about this one.
		waiting := slices.DeleteFunc(slices.Clone(step.Waiting), func(e string) bool { return e == "PHalt" })
		d.side("muted", x, y, "receive "+strings.Join(waiting, " | "), colW-labelGo*2)
	case "RaiseEvent", "MonitorRaiseEvent":
		d.dot("", x, y)
		d.side("accent", x, y, "raise "+step.Event, colW-labelGo*2)
	case "Print":
		d.dot("", x, y)
		d.side("print", x, y, strconv.Quote(step.Log), 2*colW)
	case "AssertionFailure":
		d.printf(`<circle class="fail" cx="%g" cy="%g" r="7"/><path class="fail-x" d="M%g %gl6 6m0 -6l-6 6"/>`, x, y, x-3, y-3)
		d.side("fail-text", x, y, step.Error, 6*colW)
	case "Halt":
		d.printf(`<circle class="halt" cx="%g" cy="%g" r="6"/><path class="halt-x" d="M%g %gl6 6m0 -6l-6 6"/>`, x, y, x-3, y-3)
		d.side("muted", x, y, "halt", colW-labelGo*2)
	default:
		d.dot("", x, y)
		d.side("muted", x, y, step.Log, colW-labelGo*2)
	}
	d.printf("</g>\n")
}

// send draws an event's arrow, from where it was sent to where it was
// dequeued: across rows if it waited, dashed to the bottom if it never was
// dequeued or that's past the last step drawn, or a short stub if its
// target isn't drawn.
func (d *diagram) send(row, i int) {
	step := d.s.Steps[i]
	x1, y1 := d.x(step.Sender), d.y(row)
	_, shown := d.col[step.Target]
	to := d.s.Delivered(i)
	toRow, drawn := d.rows[to]
	span := float64(colW - labelGo*2)
	if shown {
		span = max(span, abs(d.x(step.Target)-x1)-labelGo*2)
	}
	text := d.event(step, span)
	switch {
	case !shown:
		d.arrow("send", x1, y1, x1+colW/2, y1)
		d.label("", x1, x1+colW/2, y1, d.event(step, colW-labelGo*2-float64(len(step.Target)+3)*charW)+" → "+step.Target)
	case drawn && step.Target == step.Sender:
		// A loop out to the right and back.
		y2 := d.y(toRow)
		d.printf(`<path class="arrow send" d="M%g %gC%g %g %g %g %g %g" marker-end="url(#head-send)"/>`, x1, y1, x1+40, y1, x1+40, y2, x1+4, y2)
		d.side("", x1, y1, text, colW-labelGo*2)
	case drawn:
		d.arrow("send", x1, y1, d.x(step.Target), d.y(toRow))
		d.label("", x1, d.x(step.Target), y1, text)
	default:
		d.arrow("send open", x1, y1, d.x(step.Target), d.bottom())
		d.label("", x1, d.x(step.Target), y1, text)
	}
	d.dot("", x1, y1)
}

// dequeue marks where an event arrived, with a target for its tooltip.
// If its send isn't drawn, a stub says what it was.
func (d *diagram) dequeue(i int, x, y float64) {
	step := d.s.Steps[i]
	d.printf(`<circle class="hit" cx="%g" cy="%g" r="8"/>`, x, y)
	for from := 0; from < i; from++ {
		if d.s.Delivered(from) == i {
			if _, drawn := d.rows[from]; drawn {
				return
			}
			break
		}
	}
	d.arrow("send open", x-colW/2+labelGo, y, x, y)
	d.side("muted", x, y, step.Event, colW-labelGo*2)
}

func (d *diagram) arrow(class string, x1, y1, x2, y2 float64) {
	// Stop short of the lifeline, so the head shows.
	if x2 != x1 {
		if x2 > x1 {
			x2 -= 4
		} else {
			x2 += 4
		}
	}
	head := strings.Fields(class)[0]
	d.printf(`<line class="arrow %s" x1="%g" y1="%g" x2="%g" y2="%g" marker-end="url(#head-%s)"/>`, class, x1, y1, x2, y2, head)
}

// label writes text just above an arrow from x1 toward x2, at its start.
func (d *diagram) label(class string, x1, x2, y float64, text string) {
	text = fit(text, max(abs(x2-x1), colW)-labelGo*2)
	anchor, x := "start", x1+labelGo
	if x2 < x1 {
		anchor, x = "end", x1-labelGo
	} else {
		d.right = max(d.right, x+width(text))
	}
	d.printf(`<text class="label %s" x="%g" y="%g" text-anchor="%s">%s</text>`, class, x, y-5, anchor, esc(text))
}

// side writes text beside a lifeline, to its right.
func (d *diagram) side(class string, x, y float64, text string, room float64) {
	text = fit(text, room)
	d.right = max(d.right, x+labelGo+2+width(text))
	d.printf(`<text class="label %s" x="%g" y="%g">%s</text>`, class, x+labelGo+2, y+4, esc(text))
}

// event describes a step's event and payload in room: with the payload's
// field names if they fit, then without, then as "(…)", so the event's
// name is kept whole if it can be.
func (d *diagram) event(step trace.Step, room float64) string {
	named, plain := payload(step.Payload)
	for _, text := range []string{step.Event + named, step.Event + plain} {
		if width(text) <= room {
			return text
		}
	}
	if plain != "" && width(step.Event+" (…)") <= room {
		return step.Event + " (…)"
	}
	return fit(step.Event, room)
}

func width(text string) float64 {
	return float64(len([]rune(text))) * charW
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func (d *diagram) dot(class string, x, y float64) {
	d.printf(`<circle class="dot %s" cx="%g" cy="%g" r="3"/>`, class, x, y)
}

// pill writes a state's name on a lifeline.
func (d *diagram) pill(x, y float64, state string) {
	state = fit(state, colW-24)
	w := float64(len(state))*charW + 12
	d.printf(`<rect class="pill" x="%g" y="%g" width="%g" height="18" rx="9"/><text class="pill-text" x="%g" y="%g">%s</text>`, x-w/2, y-9, w, x, y+4, esc(state))
}

// payload renders an event's payload for a label, twice: named, with a
// named tuple's fields as (name = value, ...), and plain, with only the
// values. Anything else is its JSON, and null is nothing.
func payload(raw json.RawMessage) (named, plain string) {
	// PChecker writes no payload as null, or as the string "null".
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `"null"` {
		return "", ""
	}
	whole := " (" + plainValue(raw) + ")"
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	if open, err := dec.Token(); err != nil || open != json.Delim('{') {
		return whole, whole
	}
	var names, values []string
	for dec.More() {
		key, err := dec.Token()
		var v json.RawMessage
		if err != nil || dec.Decode(&v) != nil {
			return whole, whole
		}
		names = append(names, fmt.Sprint(key)+" = "+plainValue(v))
		values = append(values, plainValue(v))
	}
	return " (" + strings.Join(names, ", ") + ")", " (" + strings.Join(values, ", ") + ")"
}

// plainValue renders a payload value: strings without quotes, as P
// prints machine names, and anything else as its JSON.
func plainValue(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// fit shortens text to fit width, with an ellipsis.
func fit(text string, width float64) string {
	n := int(width / charW)
	r := []rune(text)
	if len(r) <= n || n < 2 {
		return text
	}
	return string(r[:n-1]) + "…"
}

var escaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

func esc(s string) string {
	return escaper.Replace(s)
}

const style = `<defs>
<marker id="head-send" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto"><path d="M0 0L10 5L0 10z" class="head send"/></marker>
<marker id="head-create" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto"><path d="M0 0L10 5L0 10z" class="head create"/></marker>
<marker id="head-observe" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto"><path d="M0 0L10 5L0 10z" class="head observe"/></marker>
</defs>
<style>
svg { --bg: #ffffff; --fg: #1f2328; --muted: #6e7781; --line: #d0d7de; --band: #f6f8fa; --accent: #0969da; --mon: #8250df; --fail: #cf222e; --fail-bg: #ffebe9; --pill: #ddf4ff; }
@media (prefers-color-scheme: dark) {
  svg { --bg: #0d1117; --fg: #e6edf3; --muted: #8d96a0; --line: #30363d; --band: #161b22; --accent: #4493f8; --mon: #ab7df8; --fail: #f85149; --fail-bg: #3c1618; --pill: #12263f; }
}
text { font: 12px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; fill: var(--fg); }
.bg { fill: var(--bg); }
.band { fill: var(--band); }
.fail-band { fill: var(--fail-bg); }
.num { fill: var(--muted); text-anchor: end; }
.life { stroke: var(--line); stroke-width: 2; }
.life.mon { stroke-dasharray: 4 4; }
.name rect { fill: var(--bg); stroke: var(--fg); }
.name.mon rect { stroke: var(--mon); stroke-dasharray: 4 3; }
.name text { text-anchor: middle; font-weight: 600; }
.name.repeat { opacity: 0.8; }
.name.mon text { fill: var(--mon); }
.arrow { stroke: var(--accent); stroke-width: 1.5; fill: none; }
.arrow.open { stroke-dasharray: 6 4; }
.arrow.create { stroke: var(--muted); stroke-dasharray: 2 3; }
.arrow.observe { stroke: var(--mon); stroke-dasharray: 2 3; }
.head.send { fill: var(--accent); }
.head.create { fill: var(--muted); }
.head.observe { fill: var(--mon); }
.dot { fill: var(--fg); }
.dot.mon { fill: var(--mon); }
.pill { fill: var(--pill); stroke: var(--accent); }
.pill-text { text-anchor: middle; font-size: 11px; }
.label { paint-order: stroke; stroke: var(--bg); stroke-width: 4px; stroke-linejoin: round; }
.label.muted { fill: var(--muted); }
.label.mon { fill: var(--mon); }
.label.accent { fill: var(--accent); }
.label.print { font-style: italic; }
.fail { fill: var(--fail); }
.fail-x, .halt-x { stroke: var(--bg); stroke-width: 2; }
.fail-text { fill: var(--fail); font-weight: 600; }
.halt { fill: var(--muted); }
.hit, .row { fill: transparent; }
.step:hover .arrow { stroke-width: 3; }
.step:hover text.label { text-decoration: underline; }
</style>
`
