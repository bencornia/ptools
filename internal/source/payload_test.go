package source

import (
	"os"
	"path/filepath"
	"testing"
)

const sends = `
enum tStatus { SUCCESS, ERROR }
enum tCode { LOW = 5, HIGH = 10 }
event eResponse: (status: tStatus, greeting: string);
event eCode: tCode;
event ePair: (int, bool);

machine Server {
    start state Serving {
        on eResponse do (r: (status: tStatus, greeting: string)) {
            var c: machine;
            send c, eResponse, (status = SUCCESS, greeting = "hello");
            send c, eResponse, (status = ERROR, greeting = "");
            send c, eResponse, (status = r.status, greeting = r.greeting);
            send c, eCode, HIGH;
            send c, ePair, (-3, true);
            send c, ePair, 4, false;
        }
    }
}
`

func TestPayloadAgrees(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sends.p"), []byte(sends), 0o644); err != nil {
		t.Fatal(err)
	}
	x, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	handler := x.Machines["Server"].Node
	responses := x.Statements(handler, "send_statement", "eResponse")
	if len(responses) != 3 {
		t.Fatalf("%d sends of eResponse, not 3", len(responses))
	}
	codes := x.Statements(handler, "send_statement", "eCode")
	pairs := x.Statements(handler, "send_statement", "ePair")
	for _, c := range []struct {
		send      Node
		payload   string
		agrees    bool
		confirmed int
	}{
		{responses[0], `{"status": 0, "greeting": "hello"}`, true, 2},
		{responses[0], `{"status": 1, "greeting": ""}`, false, 0},
		{responses[1], `{"status": 1, "greeting": ""}`, true, 2},
		{responses[2], `{"status": 1, "greeting": ""}`, true, 0}, // computed
		{codes[0], `10`, true, 1},
		{codes[0], `5`, false, 0},
		{pairs[0], `[-3, true]`, true, 2},
		{pairs[0], `[-3, false]`, false, 0},
		{pairs[1], `[4, false]`, true, 2},
		{responses[0], `"null"`, true, 0}, // not a tuple: nothing to compare
	} {
		agrees, confirmed := x.PayloadAgrees(c.send, []byte(c.payload))
		if agrees != c.agrees || confirmed != c.confirmed {
			t.Errorf("%s with %s: agrees %v, confirmed %d; want %v, %d",
				c.send.text(c.send.Node), c.payload, agrees, confirmed, c.agrees, c.confirmed)
		}
	}
}
