package analysis

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const signatureTypes = `type tConfig = (n: int);
// A request.
event eReq: (amount: int, source: machine);
event ePing: int;
event eDone;
// Adds two numbers.
fun Add(a: int, b: int): int { return a + b; }
fun Neg(x: int): int { return -x; }
fun Take(t: (a: int, b: int)) {}
fun Now(): int;
interface IServer(tConfig) receives eReq;
machine Server {
  start state Init {
    entry (cfg: tConfig) {}
  }
}
`

// signatureAt returns the signature help at the | in body, which goes in a
// handler, rendered as the label with the active parameter in [brackets].
func signatureAt(t *testing.T, body string) string {
	t.Helper()
	text := "machine Client {\n  var x: int;\n  var m: machine;\n  start state S {\n    entry {\n      " + body + "\n}\n"
	cursor := strings.Index(text, "|")
	text = text[:cursor] + text[cursor+1:]
	doc := parse(t, text)
	pos := doc.Position(pointAt(doc, uint(cursor)))
	help := SignatureHelp([]*Document{parse(t, signatureTypes), doc}, doc, pos)
	if help == nil {
		return ""
	}
	sig := help.Signatures[0]
	label := sig.Label
	if help.ActiveParameter != nil {
		r := sig.Parameters[*help.ActiveParameter].Label.([]protocol.UInteger)
		label = label[:r[0]] + "[" + label[r[0]:r[1]] + "]" + label[r[1]:]
	}
	return label
}

func TestSignatureHelp(t *testing.T) {
	tests := []struct{ name, body, want string }{
		{"first argument", "x = Add(|", "fun Add([a: int], b: int): int"},
		{"second argument", "x = Add(1, |", "fun Add(a: int, [b: int]): int"},
		{"inner call", "x = Add(Neg(|", "fun Neg([x: int]): int"},
		{"after an inner call", "x = Add(Neg(1), |", "fun Add(a: int, [b: int]): int"},
		{"inside a tuple argument", "Take((a = 1, |", "fun Take([t: (a: int, b: int)])"},
		{"commas in strings don't count", `x = Add("a,(b", |`, "fun Add(a: int, [b: int]): int"},
		{"commas in comments don't count", "x = Add(1, // a, b\n        |", "fun Add(a: int, [b: int]): int"},
		{"extra arguments stay on the last", "x = Add(1, 2, |", "fun Add(a: int, [b: int]): int"},
		{"no parameters", "x = Now(|", "fun Now(): int"},
		{"new machine", "m = new Server(|", "new Server([cfg: tConfig])"},
		{"new interface", "m = new IServer(|", "new IServer([tConfig])"},
		{"send payload", "send m, eReq, |", "event eReq: [(amount: int, source: machine)]"},
		{"send payload tuple", "send m, eReq, (amount = 1, |", "event eReq: [(amount: int, source: machine)]"},
		{"raise payload", "raise ePing, |", "event ePing: [int]"},
		{"announce payload", "announce ePing, |", "event ePing: [int]"},
		{"event without a payload", "raise eDone, |", "event eDone: [no payload]"},
		{"still typing the event", "send m, eRe|", ""},
		{"not in a call", "x = 1|", ""},
		{"after a finished call", "x = Add(1, 2); |", ""},
		{"previous statement's send", "send m, eReq, (amount = 1, source = this); x = |", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := signatureAt(t, tt.body); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSignatureHelpDocumentation(t *testing.T) {
	text := "fun F() {\n  Add(1, 2);\n}\n"
	doc := parse(t, text)
	help := SignatureHelp([]*Document{parse(t, signatureTypes), doc}, doc, protocol.Position{Line: 1, Character: 6})
	if help == nil || help.Signatures[0].Documentation.(protocol.MarkupContent).Value != "Adds two numbers." {
		t.Errorf("got %+v", help)
	}
}
