package analysis

import (
	"fmt"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const tokensMain = `/* Two
   lines. */
event ePing: int;
type tPair = (first: int, second: float);
enum tColor { RED }

machine Client {
  var server: machine;
  start state Init {
    entry (n: int) {
      var p: tPair;
      p.first = n + 1;
      send server, ePing, 2;
      print "hi";
      goto Init;
    }
  }
  fun F(): int { return unknown; }
}
`

// decodeTokens renders encoded tokens as "text:type", with "+decl" for
// declarations.
func decodeTokens(t *testing.T, doc *Document, data []protocol.UInteger) []string {
	t.Helper()
	if len(data)%5 != 0 {
		t.Fatalf("%d integers isn't a whole number of tokens", len(data))
	}
	lines := strings.Split(string(doc.Text), "\n")
	got := []string{}
	var line, char protocol.UInteger
	for i := 0; i < len(data); i += 5 {
		if data[i] > 0 {
			char = 0
		}
		line += data[i]
		char += data[i+1]
		text := lines[line][char : char+data[i+2]] // ASCII, so UTF-16 columns are bytes
		token := fmt.Sprintf("%s:%s", text, TokenLegend.TokenTypes[data[i+3]])
		if data[i+4]&modifierDeclaration != 0 {
			token += "+decl"
		}
		got = append(got, token)
	}
	return got
}

func TestSemanticTokens(t *testing.T) {
	doc := parse(t, tokensMain)
	got := decodeTokens(t, doc, SemanticTokens([]*Document{doc}, doc, nil))
	want := []string{
		"/* Two:comment", "   lines. */:comment",
		"event:keyword", "ePing:event+decl", "int:type",
		"type:keyword", "tPair:type+decl", "first:property+decl", "int:type", "second:property+decl", "float:type",
		"enum:keyword", "tColor:enum+decl", "RED:enumMember+decl",
		"machine:keyword", "Client:class+decl",
		"var:keyword", "server:property+decl", "machine:type",
		"start:keyword", "state:keyword", "Init:namespace+decl",
		"entry:keyword", "n:parameter+decl", "int:type",
		"var:keyword", "p:variable+decl", "tPair:type",
		"p:variable", "first:property", "n:parameter", "1:number",
		"send:keyword", "server:property", "ePing:event", "2:number",
		"print:keyword", `"hi":string`,
		"goto:keyword", "Init:namespace",
		"fun:keyword", "F:function+decl", "int:type", "return:keyword",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestSemanticTokensInRange(t *testing.T) {
	doc := parse(t, tokensMain)
	rng := protocol.Range{Start: protocol.Position{Line: 11, Character: 0}, End: protocol.Position{Line: 13, Character: 0}}
	got := decodeTokens(t, doc, SemanticTokens([]*Document{doc}, doc, &rng))
	want := "p:variable first:property n:parameter 1:number send:keyword server:property ePing:event 2:number"
	if strings.Join(got, " ") != want {
		t.Errorf("got %s\nwant %s", strings.Join(got, " "), want)
	}
}

func TestHighlight(t *testing.T) {
	doc := parse(t, tokensMain)
	got := []string{}
	for _, tok := range Highlight([]*Document{doc}, doc) {
		s := fmt.Sprintf("%s:%s", doc.Text[tok.Start:tok.End], tok.Type)
		if tok.Declaration {
			s += "+decl"
		}
		got = append(got, s)
	}

	// The same tokens as SemanticTokens, except that a comment spanning
	// lines stays one token.
	want := decodeTokens(t, doc, SemanticTokens([]*Document{doc}, doc, nil))
	if want[0] != "/* Two:comment" || want[1] != "   lines. */:comment" {
		t.Fatalf("SemanticTokens starts %q", want[:2])
	}
	want = append([]string{"/* Two\n   lines. */:comment"}, want[2:]...)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("Highlight =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
