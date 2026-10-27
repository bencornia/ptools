package analysis

import (
	"fmt"
	"strings"
	"testing"
)

func TestReferences(t *testing.T) {
	parser := NewParser()
	t.Cleanup(parser.Close)
	types := Parse(parser, typesURI, 1, typesFile)
	t.Cleanup(types.Close)
	client := Parse(parser, clientURI, 1, clientFile)
	t.Cleanup(client.Close)
	docs := []*Document{client, types}
	workspaceOf := func(*Document) []*Document { return docs }
	text := map[string]string{typesURI: typesFile, clientURI: clientFile}

	// spot is a place in a file: delta characters into the occurrence-th
	// match of needle.
	type spot struct {
		uri, needle string
		occurrence  int
		delta       int
	}
	render := func(s spot) string {
		pos := at(t, text[s.uri], s.needle, s.occurrence, s.delta)
		return fmt.Sprintf("%s:%d:%d", strings.TrimPrefix(s.uri, "file:///"), pos.Line+1, pos.Character+1)
	}

	tests := []struct {
		name               string
		cursor             spot
		includeDeclaration bool
		want               []spot
	}{
		{
			// Not Finish's local amount, nor the tuple field `amount = ...`.
			name:               "machine variable",
			cursor:             spot{clientURI, "amount: int;\n\n", 1, 0},
			includeDeclaration: true,
			want: []spot{
				{clientURI, "amount: int;\n\n", 1, 0},
				{clientURI, "= amount)", 1, 2},
			},
		},
		{
			// The tuple value's field refers to it; `req.amount` in Finish
			// doesn't resolve, since req isn't declared there.
			name:               "tuple field",
			cursor:             spot{typesURI, "amount:", 1, 0},
			includeDeclaration: true,
			want: []spot{
				{clientURI, "amount = amount", 1, 0},
				{typesURI, "amount:", 1, 0},
			},
		},
		{
			// Client's Wait, including `c is Wait` for c: Client, but not
			// Server's state of the same name.
			name:               "state",
			cursor:             spot{clientURI, "goto Wait", 1, 5},
			includeDeclaration: true,
			want: []spot{
				{clientURI, "goto Wait", 1, 5},
				{clientURI, "state Wait {\n", 1, 6},
				{clientURI, "is Wait", 1, 3},
			},
		},
		{
			name:               "from a use, across files",
			cursor:             spot{clientURI, "eRequest, req", 1, 0},
			includeDeclaration: true,
			want: []spot{
				{clientURI, "eRequest, req", 1, 0},
				{typesURI, "eRequest", 1, 0},
			},
		},
		{
			name:               "without the declaration",
			cursor:             spot{typesURI, "eRequest", 1, 0},
			includeDeclaration: false,
			want: []spot{
				{clientURI, "eRequest, req", 1, 0},
			},
		},
		{
			name:               "module, not the machine of the same name",
			cursor:             spot{clientURI, "union Client", 1, 6},
			includeDeclaration: true,
			want: []spot{
				{clientURI, "module Client", 1, 7},
				{clientURI, "union Client", 1, 6},
			},
		},
		{
			name:               "invariant in a Lemma",
			cursor:             spot{clientURI, "waiting:", 1, 0},
			includeDeclaration: true,
			want: []spot{
				{clientURI, "waiting:", 1, 0},
				{clientURI, "lemmas_waiting", 1, 0},
			},
		},
		{
			name:   "nothing under the cursor",
			cursor: spot{clientURI, "{0}", 1, 0},
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := map[string]*Document{typesURI: types, clientURI: client}[tt.cursor.uri]
			pos := at(t, text[tt.cursor.uri], tt.cursor.needle, tt.cursor.occurrence, tt.cursor.delta)
			got := []string{}
			for _, loc := range References(docs, workspaceOf, doc, pos, tt.includeDeclaration) {
				got = append(got, fmt.Sprintf("%s:%d:%d", strings.TrimPrefix(loc.URI, "file:///"), loc.Range.Start.Line+1, loc.Range.Start.Character+1))
			}
			want := []string{}
			for _, s := range tt.want {
				want = append(want, render(s))
			}
			if strings.Join(got, " ") != strings.Join(want, " ") {
				t.Errorf("references\n got %v\nwant %v", got, want)
			}
		})
	}
}

func TestMachineReferences(t *testing.T) {
	parser := NewParser()
	t.Cleanup(parser.Close)
	types := Parse(parser, typesURI, 1, typesFile)
	t.Cleanup(types.Close)
	client := Parse(parser, clientURI, 1, clientFile)
	t.Cleanup(client.Close)
	docs := []*Document{client, types}

	// Every `Client` in both files except the module's name and its use in
	// `union Client`.
	pos := at(t, clientFile, "machine Client", 1, 8)
	got := References(docs, func(*Document) []*Document { return docs }, client, pos, true)

	all := strings.Count(clientFile, "Client") + strings.Count(typesFile, "Client")
	if want := all - 2; len(got) != want {
		t.Errorf("got %d references, want %d: %+v", len(got), want, got)
	}
	for _, loc := range got {
		if loc.URI == clientURI && (loc.Range.Start == at(t, clientFile, "module Client", 1, 7) ||
			loc.Range.Start == at(t, clientFile, "union Client", 1, 6)) {
			t.Errorf("module reference included: %+v", loc)
		}
	}
}
