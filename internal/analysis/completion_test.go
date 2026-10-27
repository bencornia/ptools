package analysis

import (
	"slices"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const completionTypes = `// A request from a client.
type tReq = (source: machine, amount: int);
event eReq: tReq;
event ePing;
eventset eAll = { eReq, ePing };
enum tColor { RED, GREEN }

spec Watch observes eReq {
  var seen: int;
  start state S {}
}
`

const completionMain = `machine Client {
  var server: machine;
  var count: int;

  start state Init {
    entry (payload: tReq) {
      var req: tReq;
      var server: int;
      CURSOR
    }
  }

  state Done {}

  fun Inc(x: int): int {
    return x + 1;
  }
}

machine Other {
  var unrelated: int;
}
`

// complete returns the labels Completion offers with cursor in place of
// CURSOR in completionMain, without keywords, and whether keywords came too.
func complete(t *testing.T, cursor string) (labels []string, keywordsToo bool) {
	t.Helper()
	parser := NewParser()
	t.Cleanup(parser.Close)
	types := Parse(parser, typesURI, 1, completionTypes)
	t.Cleanup(types.Close)
	if diags := SyntaxDiagnostics(types); len(diags) > 0 {
		t.Fatalf("test source has syntax errors: %+v", diags)
	}

	before, after, _ := strings.Cut(cursor, "|")
	text := strings.Replace(completionMain, "CURSOR", before+after, 1)
	main := Parse(parser, clientURI, 1, text)
	t.Cleanup(main.Close)
	head := text[:strings.Index(completionMain, "CURSOR")+len(before)]
	line := strings.Count(head, "\n")
	pos := protocol.Position{Line: protocol.UInteger(line), Character: protocol.UInteger(len(head) - strings.LastIndex(head, "\n") - 1)}

	labels = []string{}
	for _, item := range Completion(parser, []*Document{types, main}, main, pos) {
		if *item.Kind == protocol.CompletionItemKindKeyword {
			keywordsToo = true
			continue
		}
		labels = append(labels, item.Label)
	}
	slices.Sort(labels)
	return labels, keywordsToo
}

func TestCompletion(t *testing.T) {
	tests := []struct {
		name     string
		cursor   string
		want     []string
		keywords bool
	}{
		{"fields after a dot", "req.|", []string{"amount", "source"}, false},
		{"fields with part of the name typed", "req.am|", []string{"amount", "source"}, false},
		{"fields of a parameter, with no statement after", "payload.|", []string{"amount", "source"}, false},
		{"a spec's variables", "Watch.|", []string{"seen"}, false},
		{"an event's payload fields", "var e: eReq;\n      e.|", []string{"amount", "source"}, false},
		{"an event set's payload fields", "var e: eAll;\n      e.|", []string{"amount", "source"}, false},
		{"fields of a tuple value", "req = (source = this, |);", []string{"amount", "source"}, false},
		{"the value a field is read from", "|.amount;", []string{
			"Client", "GREEN", "Inc", "Other", "RED", "Watch",
			"count", "eAll", "ePing", "eReq", "payload", "req", "server", "tColor", "tReq",
		}, true},
		{"states after goto", "goto |", []string{"Done", "Init"}, false},
		{"names in scope", "|", []string{
			"Client", "GREEN", "Inc", "Other", "RED", "Watch",
			"count", "eAll", "ePing", "eReq", "payload", "req", "server", "tColor", "tReq",
		}, true},
		{"declaring a variable", "var |", nil, false},
		{"in a comment", "// |", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, keywordsToo := complete(t, tt.cursor)
			if strings.Join(got, " ") != strings.Join(tt.want, " ") || keywordsToo != tt.keywords {
				t.Errorf("got %v (keywords %v), want %v (keywords %v)", got, keywordsToo, tt.want, tt.keywords)
			}
		})
	}
}

func TestCompletionItems(t *testing.T) {
	parser := NewParser()
	t.Cleanup(parser.Close)
	types := Parse(parser, typesURI, 1, completionTypes)
	t.Cleanup(types.Close)
	text := strings.Replace(completionMain, "CURSOR", "", 1)
	main := Parse(parser, clientURI, 1, text)
	t.Cleanup(main.Close)

	items := map[string]protocol.CompletionItem{}
	for _, item := range Completion(parser, []*Document{types, main}, main, at(t, text, "\n    }\n  }", 1, 0)) {
		items[item.Label] = item
	}
	tests := []struct {
		label, detail string
		kind          protocol.CompletionItemKind
		doc           string
	}{
		// The local hides the machine's variable.
		{"server", "var server: int", protocol.CompletionItemKindVariable, ""},
		{"count", "var count: int", protocol.CompletionItemKindField, ""},
		{"payload", "payload: tReq", protocol.CompletionItemKindVariable, ""},
		{"Inc", "fun Inc(x: int): int", protocol.CompletionItemKindFunction, ""},
		{"tReq", "type tReq = (source: machine, amount: int);", protocol.CompletionItemKindStruct, "A request from a client."},
		{"eReq", "event eReq: tReq;", protocol.CompletionItemKindEvent, ""},
		{"RED", "enum tColor { RED, GREEN }", protocol.CompletionItemKindEnumMember, ""},
		{"Client", "machine Client", protocol.CompletionItemKindClass, ""},
		{"while", "", protocol.CompletionItemKindKeyword, ""},
	}
	for _, tt := range tests {
		item, ok := items[tt.label]
		if !ok {
			t.Errorf("no item for %s", tt.label)
			continue
		}
		detail, doc := "", ""
		if item.Detail != nil {
			detail = *item.Detail
		}
		if item.Documentation != nil {
			doc = item.Documentation.(protocol.MarkupContent).Value
		}
		if detail != tt.detail || *item.Kind != tt.kind || doc != tt.doc {
			t.Errorf("%s: got %q, kind %d, doc %q; want %q, kind %d, doc %q", tt.label, detail, *item.Kind, doc, tt.detail, tt.kind, tt.doc)
		}
	}
}
