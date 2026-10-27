package analysis

import (
	"fmt"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const foldingMain = `// The client.
// Sends requests.
machine Client {
  var server: machine; // trailing
  /* A block
     comment. */
  start state Init {
    entry {
      send server, ePing;
    }
  }
  state Done { }
}

// Alone.
eventset eAll = {
  ePing,
  ePong
};
`

func TestFoldingRanges(t *testing.T) {
	doc := parse(t, foldingMain)
	got := []string{}
	for _, f := range FoldingRanges(doc) {
		kind := ""
		if f.Kind != nil {
			kind = " " + *f.Kind
		}
		got = append(got, fmt.Sprintf("%d-%d%s", f.StartLine+1, f.EndLine+1, kind))
	}
	want := []string{
		"1-2 comment", // the two line comments
		"3-12",        // machine
		"5-6 comment", // block comment
		"7-10",        // state
		"8-9",         // entry
		"16-18",       // event set
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

func TestSelectionRanges(t *testing.T) {
	doc := parse(t, foldingMain)
	pos := at(t, foldingMain, "ePing;", 1, 2)
	selections := SelectionRanges(doc, []protocol.Position{pos})
	if len(selections) != 1 {
		t.Fatalf("got %d selections for 1 position", len(selections))
	}

	got := []string{}
	for s := &selections[0]; s != nil; s = s.Parent {
		text := string(doc.Text[doc.offset(s.Range.Start):doc.offset(s.Range.End)])
		if lines := strings.Split(text, "\n"); len(lines) > 1 {
			text = lines[0] + " … " + strings.TrimSpace(lines[len(lines)-1])
		}
		got = append(got, text)
	}
	want := []string{
		"ePing",
		"send server, ePing;",
		"{ … }",       // the handler's body
		"entry { … }", // the entry handler
		"start state Init { … }",
		"{ … }", // the machine's body
		"machine Client { … }",
		"// The client. … ",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}
