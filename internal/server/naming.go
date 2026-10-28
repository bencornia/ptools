package server

import (
	"fmt"
	"path/filepath"

	"github.com/bencornia/p-lsp/internal/analysis"
	"github.com/bencornia/p-lsp/internal/style"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// naming returns the naming style for the file at path: from the nearest
// .plint.toml in its directory or above, or the default. A file that can't
// be read is logged, once until it changes, and the default used. Callers
// must hold mu.
func (s *Server) naming(path string) style.Naming {
	if !filepath.IsAbs(path) {
		return style.Default().Naming
	}
	file := style.Find(filepath.Dir(path))
	if file == "" {
		return style.Default().Naming
	}
	config, err := style.Load(file)
	if err != nil {
		if s.styleErrors == nil {
			s.styleErrors = map[string]string{}
		}
		if s.styleErrors[file] != err.Error() {
			s.styleErrors[file] = err.Error()
			log.Errorf("using the default naming style: %s", err)
		}
		return style.Default().Naming
	}
	delete(s.styleErrors, file)
	return config.Naming
}

// renameActions offers to rename each badly named declaration in rng to its
// name in the style, everywhere it's used, if renaming it is safe. Callers
// must hold mu.
func (s *Server) renameActions(path string, doc *analysis.Document, rng protocol.Range) []protocol.CodeAction {
	actions := []protocol.CodeAction{}
	kind := protocol.CodeActionKindQuickFix
	for _, p := range analysis.Naming(doc, s.naming(path)) {
		if p.Suggestion == "" || !overlaps(p.Diagnostic.Range, rng) {
			continue
		}
		changes, err := analysis.Rename(s.allDocs(), s.workspaceOf(), doc, p.Diagnostic.Range.Start, p.Suggestion)
		if err != nil {
			// A clash with another name, say; the warning stays, unfixed.
			log.Infof("not offering to rename %s: %s", p.Name, err)
			continue
		}
		edit := protocol.WorkspaceEdit{Changes: map[protocol.DocumentUri][]protocol.TextEdit{}}
		for uri, edits := range changes {
			edit.Changes[uri] = edits
		}
		actions = append(actions, protocol.CodeAction{
			Title:       fmt.Sprintf("Rename `%s` to `%s`", p.Name, p.Suggestion),
			Kind:        &kind,
			Diagnostics: []protocol.Diagnostic{p.Diagnostic},
			Edit:        &edit,
		})
	}
	return actions
}

// openDiagnostics returns the diagnostics to republish for every open file,
// as after its naming style changes. Callers must hold mu.
func (s *Server) openDiagnostics() []protocol.PublishDiagnosticsParams {
	var notifications []protocol.PublishDiagnosticsParams
	for path := range s.open {
		if doc, ok := s.docs[path]; ok {
			notifications = append(notifications, protocol.PublishDiagnosticsParams{URI: doc.URI, Diagnostics: s.diagnosticsFor(path)})
		}
	}
	return notifications
}
