package server

import (
	"path/filepath"

	"github.com/bencornia/ptools/internal/analysis"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// The commands of the lenses over each test case. The client runs them;
// the VS Code extension in editors/vscode does. Each takes one argument,
// a TestArgument.
const (
	RunTestCommand   = "p.runTest"
	DebugTestCommand = "p.debugTest"
)

// TestArgument says which test case a lens's command is for: its name, the
// file declaring it, and its project's directory, which holds the .pproj
// that compiles it, or "" if no project lists the file.
type TestArgument struct {
	Name    string `json:"name"`
	File    string `json:"file"`
	Project string `json:"project,omitempty"`
}

// codeLens puts Run test and Debug test above each test case.
func (s *Server) codeLens(_ *glsp.Context, params *protocol.CodeLensParams) ([]protocol.CodeLens, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lenses := []protocol.CodeLens{}
	path := key(params.TextDocument.URI)
	doc, ok := s.docs[path]
	if !ok {
		return lenses, nil
	}
	project := ""
	if owners := s.projects.Owners(path); len(owners) > 0 {
		project = filepath.Dir(owners[0])
	}
	for _, test := range analysis.TestCases(doc) {
		arg := TestArgument{Name: test.Name, File: uriToPath(doc.URI), Project: project}
		lenses = append(lenses,
			protocol.CodeLens{Range: test.Range, Command: &protocol.Command{Title: "Run test", Command: RunTestCommand, Arguments: []any{arg}}},
			protocol.CodeLens{Range: test.Range, Command: &protocol.Command{Title: "Debug test", Command: DebugTestCommand, Arguments: []any{arg}}},
		)
	}
	return lenses, nil
}
