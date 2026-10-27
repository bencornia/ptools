package analysis

import (
	"fmt"
	"strings"
	"testing"
)

func TestWorkspaceSymbols(t *testing.T) {
	parser := NewParser()
	t.Cleanup(parser.Close)
	types := Parse(parser, typesURI, 1, typesFile)
	t.Cleanup(types.Close)
	client := Parse(parser, clientURI, 1, clientFile)
	t.Cleanup(client.Close)
	docs := []*Document{types, client}

	// describe renders symbols as "name (container) file:line".
	describe := func(query string, limit int) string {
		var parts []string
		for _, s := range WorkspaceSymbols(docs, query, limit) {
			container := ""
			if s.ContainerName != nil {
				container = " (" + *s.ContainerName + ")"
			}
			parts = append(parts, fmt.Sprintf("%s%s %s:%d", s.Name, container,
				strings.TrimPrefix(s.Location.URI, "file:///"), s.Location.Range.Start.Line+1))
		}
		return strings.Join(parts, ", ")
	}

	tests := []struct {
		query string
		limit int
		want  string
	}{
		// Exact, then prefix, then substring, then letters in order.
		{"erequest", 10, "eRequest types.p:3"},
		{"wait", 10, "Wait (Client) client.p:15, Wait (Server) client.p:33, waiting (lemmas) client.p:42"},
		{"req", 10, "eRequest types.p:3, tRequest types.p:1"},
		{"ersp", 10, "eResponse types.p:4"},
		// Machine members carry their machine's name.
		{"server", 10, "Server client.p:31, server (Client) client.p:2"},
		// Function locals and handlers aren't listed.
		{"amount", 10, "amount (Client) client.p:3"},
		{"entry", 10, ""},
		{"on e", 10, ""},
		{"nomatch", 10, ""},
		// The limit keeps the best matches.
		{"e", 2, "eRequest types.p:3, eResponse types.p:4"},
	}
	for _, tt := range tests {
		if got := describe(tt.query, tt.limit); got != tt.want {
			t.Errorf("query %q:\n got %s\nwant %s", tt.query, got, tt.want)
		}
	}

	if got := len(WorkspaceSymbols(docs, "", 1000)); got < 15 {
		t.Errorf("empty query returned %d symbols, want every declaration", got)
	}
}

func TestWordStarts(t *testing.T) {
	for name, want := range map[string]string{
		"eWithDrawReq":  "eWDR",
		"pendingWDReqs": "pWDR",
		"snake_case_1x": "scx",
		"Server":        "S",
	} {
		if got := wordStarts(name); got != want {
			t.Errorf("wordStarts(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestMatchScore(t *testing.T) {
	tests := []struct {
		query, name string
		score       int
		ok          bool
	}{
		{"Client", "client", 0, true},
		{"cli", "Client", 1, true},
		{"ient", "Client", 2, true},
		{"wdr", "eWithDrawReq", 3, true},
		{"wdr", "pendingWDReqs", 2, true}, // the "WDR" in WDReqs
		{"wdr", "newLeader", 4, true},
		{"gwp", "guaranteed_withdraw_progress", 3, true},
		{"ersp", "eResponse", 4, true},
		{"rdw", "eWithDrawReq", 0, false},
		{"", "anything", 1, true},
	}
	for _, tt := range tests {
		score, ok := matchScore(tt.query, tt.name)
		if ok != tt.ok || (ok && score != tt.score) {
			t.Errorf("matchScore(%q, %q) = %d, %v; want %d, %v", tt.query, tt.name, score, ok, tt.score, tt.ok)
		}
	}
}
