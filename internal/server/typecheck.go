package server

import (
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/bencornia/ptools/internal/analysis"
	"github.com/bencornia/ptools/internal/compile"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

const compileSource = "p compile"

// SetCompiler turns on type-check diagnostics: saving a file in a .pproj
// runs command (such as []string{"p"}) to compile its project. If the
// command can't be found, they stay off.
func (s *Server) SetCompiler(command []string, timeout time.Duration) {
	runner := &compile.Runner{Command: command, Timeout: timeout}
	if err := runner.Available(); err != nil {
		log.Infof("type-check diagnostics are off: %s", err)
		return
	}
	s.mu.Lock()
	s.compiler = runner
	s.mu.Unlock()
}

func (s *Server) didSave(ctx *glsp.Context, params *protocol.DidSaveTextDocumentParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.compiler == nil {
		return nil
	}
	// Type-check the projects that list the file. A file in no .pproj isn't
	// checked: which files it's compiled with would only be a guess.
	path := key(params.TextDocument.URI)
	files, _ := s.projects.Visible(path)
	for _, pproj := range s.projects.Owners(path) {
		// Compiling takes seconds, and glsp handles one message at a time.
		go s.typeCheck(ctx, s.compiler, pproj, files)
	}
	return nil
}

// typeCheck compiles a project and publishes the type errors it finds,
// replacing the project's previous ones.
func (s *Server) typeCheck(ctx *glsp.Context, compiler *compile.Runner, pproj string, files []string) {
	errs, err := compiler.Check(pproj, files)
	if errors.Is(err, compile.ErrCanceled) {
		return
	}
	if err != nil {
		// Keep the previous diagnostics: this says nothing about the code.
		log.Errorf("type-checking %s: %s", pproj, err)
		return
	}

	s.mu.Lock()
	diagnostics := map[string][]protocol.Diagnostic{}
	for _, e := range errs {
		path := e.File
		if path == "" {
			// No location: report it on the project file.
			path = pproj
		}
		diagnostics[path] = append(diagnostics[path], s.compileDiagnostic(path, e))
	}
	if s.compileDiagnostics == nil {
		s.compileDiagnostics = map[string]map[string][]protocol.Diagnostic{}
	}
	previous := s.compileDiagnostics[pproj]
	s.compileDiagnostics[pproj] = diagnostics

	// Republish every file that had or has errors from this project.
	var notifications []protocol.PublishDiagnosticsParams
	for path := range mergeKeys(previous, diagnostics) {
		uri := pathToURI(path)
		if doc, ok := s.docs[path]; ok {
			uri = doc.URI
		}
		notifications = append(notifications, protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: s.diagnosticsFor(path)})
	}
	s.mu.Unlock()

	for _, n := range notifications {
		ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, n)
	}
}

// compileDiagnostic turns a compiler error into a diagnostic, underlining
// the name at its position when the file is loaded. Callers must hold mu.
func (s *Server) compileDiagnostic(path string, e compile.Error) protocol.Diagnostic {
	pos := protocol.Position{Line: protocol.UInteger(e.Line), Character: protocol.UInteger(e.Column)}
	rng := protocol.Range{Start: pos, End: pos}
	if doc, ok := s.docs[path]; ok {
		rng = doc.WordRange(pos)
	}
	severity := protocol.DiagnosticSeverityError
	source := compileSource
	return protocol.Diagnostic{Range: rng, Severity: &severity, Source: &source, Message: e.Message}
}

// diagnosticsFor returns everything to show for the file at path: syntax
// errors and lint warnings while it's open in the editor, and type errors
// from the last compile of each project. Callers must hold mu.
func (s *Server) diagnosticsFor(path string) []protocol.Diagnostic {
	diagnostics := []protocol.Diagnostic{}
	if doc, ok := s.docs[path]; ok && s.open[path] {
		diagnostics = append(diagnostics, analysis.Diagnostics(s.visibleDocs(path), doc)...)
	}
	// The compiler's report of a problem already found as the file was
	// edited, such as an undefined event, would only repeat it.
	live := map[protocol.Range]bool{}
	for _, d := range diagnostics {
		live[d.Range] = true
	}
	for _, pproj := range slices.Sorted(maps.Keys(s.compileDiagnostics)) {
		for _, d := range s.compileDiagnostics[pproj][path] {
			if !live[d.Range] {
				diagnostics = append(diagnostics, d)
			}
		}
	}
	if doc, ok := s.docs[path]; ok && s.open[path] {
		config := s.style(path)
		diagnostics = append(diagnostics, analysis.NamingDiagnostics(doc, config.Naming)...)
		diagnostics = append(diagnostics, analysis.Checks(s.userDocs(path), doc, config.Checks)...)
	}
	return diagnostics
}

func mergeKeys[V any](a, b map[string]V) map[string]bool {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	return keys
}
