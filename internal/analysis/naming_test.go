package analysis

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bencornia/ptools/internal/style"
)

// namingSource declares every kind of name, all in the default style.
const namingSource = `type Lookup = (client: machine, lang_code: Language);
type Opaque;
enum Language { ENGLISH, NOT_LISTED = 2 }
event Lookup: Lookup;
eventset Requests = { Lookup };
interface Service(int) receives Lookup;
param given: int;

fun pick_language(n: int): Language {
  var picked: Language;
  return ENGLISH;
}

machine Client {
  var request_count: int;
  start state Init {
    entry (init: Lookup) { }
    on Lookup do (lookup: Lookup) { }
  }
  fun send_one(to_server: machine) { }
}

spec RequestCompletes observes Lookup {
  start state Waiting { }
}

module Clients = { Client };
test lookup_all [main=Client]: assert RequestCompletes in { Client };
pure is_ready(n: int): bool;
`

func TestDefaultStyleAcceptsItsOwnNames(t *testing.T) {
	doc := parse(t, namingSource)
	if problems := Naming(doc, style.Default().Naming); len(problems) > 0 {
		t.Errorf("problems in conforming source: %v", messages(problems))
	}
}

func TestNamingReportsEachKind(t *testing.T) {
	doc := parse(t, `type tLookup = (Client: machine, langCode: language);
type opaque_thing;
enum language { English, not_listed = 2 }
event lookup_done: tLookup;
eventset requests = { lookup_done };
param Given: int;

fun PickLanguage(N: int): language {
  var Picked: language;
  return English;
}

machine client {
  var requestCount: int;
  start state init {
    on lookup_done do (Lookup: tLookup) { }
  }
}

spec request_completes observes lookup_done { start state S { } }
module clients = { client };
test LookupAll [main=client]: { client };
test tcLookup [main=client]: { client };
`)
	got := messages(Naming(doc, style.Default().Naming))
	want := []string{
		"1:17 variable name `Client` should be snake_case: `client`",
		"1:34 variable name `langCode` should be snake_case: `lang_code`",
		"2:6 type name `opaque_thing` should be TitleCase: `OpaqueThing`",
		"3:6 type name `language` should be TitleCase: `Language`",
		"3:17 enum constant name `English` should be SCREAMING_CASE: `ENGLISH`",
		"3:26 enum constant name `not_listed` should be SCREAMING_CASE: `NOT_LISTED`",
		"4:7 event name `lookup_done` should be TitleCase: `LookupDone`",
		"5:10 event name `requests` should be TitleCase: `Requests`",
		"6:7 variable name `Given` should be snake_case: `given`",
		"8:5 function name `PickLanguage` should be snake_case: `pick_language`",
		"8:18 variable name `N` should be snake_case: `n`",
		"9:7 variable name `Picked` should be snake_case: `picked`",
		"13:9 machine name `client` should be TitleCase: `Client`",
		"14:7 variable name `requestCount` should be snake_case: `request_count`",
		"15:15 state name `init` should be TitleCase: `Init`",
		"16:24 variable name `Lookup` should be snake_case: `lookup`",
		"20:6 machine name `request_completes` should be TitleCase: `RequestCompletes`",
		"21:8 module name `clients` should be TitleCase: `Clients`",
		"22:6 test case name `LookupAll` should be snake_case: `lookup_all`",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestNamingPrefixesAndOff(t *testing.T) {
	src := "event eLookup;\ntest tcLookup [main=M]: { M };\nmachine M { start state S { } }\n"
	naming := style.Default().Naming
	if problems := Naming(parse(t, src), naming); len(problems) > 0 {
		t.Errorf("prefixed names reported: %v", messages(problems))
	}

	naming.Prefixes = nil
	got := messages(Naming(parse(t, src), naming))
	if len(got) != 2 {
		t.Errorf("with no prefixes allowed, got %v", got)
	}

	naming.Events, naming.TestCases = style.Off, style.Off
	if problems := Naming(parse(t, src), naming); len(problems) > 0 {
		t.Errorf("rules turned off still report: %v", messages(problems))
	}
}

func TestNamingSkipsSyntaxErrors(t *testing.T) {
	doc := parse(t, "machine bad_name {\n  start state S {\n}\nevent lower_case;\n")
	for _, p := range Naming(doc, style.Default().Naming) {
		if p.Name == "bad_name" {
			t.Errorf("reported a name inside a syntax error: %s", p.Diagnostic.Message)
		}
	}
}

func messages(problems []NamingProblem) []string {
	var out []string
	for _, p := range problems {
		start := p.Diagnostic.Range.Start
		out = append(out, fmt.Sprintf("%d:%d %s", start.Line+1, start.Character+1, p.Diagnostic.Message))
	}
	return out
}
