// Package server wires the analysis package to the language server protocol.
package server

import (
	"maps"
	"slices"
	"sync"

	"github.com/bencornia/p-lsp/internal/analysis"
	"github.com/bencornia/p-lsp/internal/project"
	"github.com/tliron/commonlog"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	glspserver "github.com/tliron/glsp/server"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

const name = "p-lsp"

var log = commonlog.GetLogger(name)

type Server struct {
	version string
	handler protocol.Handler

	// mu guards everything below. glsp currently handles messages one at a
	// time, but nothing in its API promises that.
	mu     sync.Mutex
	parser *sitter.Parser
	// docs holds every P file in the workspace and in the projects it
	// includes, keyed by path (see key). Files open in the editor use the
	// editor's text; the rest are read from disk.
	docs     map[string]*analysis.Document
	open     map[string]bool
	projects *project.Index
	// roots are the workspace folders.
	roots []string
	// watchFiles is whether the client can watch files for us.
	watchFiles bool
}

func New(version string) *Server {
	s := &Server{
		version:  version,
		parser:   analysis.NewParser(),
		docs:     map[string]*analysis.Document{},
		open:     map[string]bool{},
		projects: project.NewIndex(),
	}
	s.handler = protocol.Handler{
		Initialize:                     s.initialize,
		Initialized:                    s.initialized,
		Shutdown:                       s.shutdown,
		TextDocumentDidOpen:            s.didOpen,
		TextDocumentDidChange:          s.didChange,
		TextDocumentDidClose:           s.didClose,
		TextDocumentDocumentSymbol:     s.documentSymbol,
		TextDocumentDefinition:         s.definition,
		WorkspaceDidChangeWatchedFiles: s.didChangeWatchedFiles,
	}
	return s
}

// RunStdio serves LSP over stdin/stdout until the client disconnects.
func (s *Server) RunStdio() error {
	return glspserver.NewServer(&s.handler, name, false).RunStdio()
}

func (s *Server) initialize(_ *glsp.Context, params *protocol.InitializeParams) (any, error) {
	var roots []string
	for _, folder := range params.WorkspaceFolders {
		roots = append(roots, uriToPath(folder.URI))
	}
	if len(roots) == 0 && params.RootURI != nil {
		roots = append(roots, uriToPath(*params.RootURI))
	}
	workspace := params.Capabilities.Workspace
	watchFiles := workspace != nil && workspace.DidChangeWatchedFiles != nil &&
		workspace.DidChangeWatchedFiles.DynamicRegistration != nil && *workspace.DidChangeWatchedFiles.DynamicRegistration

	s.mu.Lock()
	s.roots = roots
	s.watchFiles = watchFiles
	s.reloadWorkspace()
	s.mu.Unlock()

	capabilities := s.handler.CreateServerCapabilities()

	// Re-parsing a whole P file takes well under a millisecond, so ask for
	// full text on every change rather than applying incremental edits.
	full := protocol.TextDocumentSyncKindFull
	capabilities.TextDocumentSync.(*protocol.TextDocumentSyncOptions).Change = &full

	return protocol.InitializeResult{
		Capabilities: capabilities,
		ServerInfo: &protocol.InitializeResultServerInfo{
			Name:    name,
			Version: &s.version,
		},
	}, nil
}

func (s *Server) shutdown(_ *glsp.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for path, doc := range s.docs {
		doc.Close()
		delete(s.docs, path)
	}
	return nil
}

func (s *Server) didOpen(ctx *glsp.Context, params *protocol.DidOpenTextDocumentParams) error {
	item := params.TextDocument
	s.update(ctx, item.URI, item.Version, item.Text)
	return nil
}

func (s *Server) didChange(ctx *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
	// With full sync, the last change holds the whole document.
	for i := len(params.ContentChanges) - 1; i >= 0; i-- {
		if change, ok := params.ContentChanges[i].(protocol.TextDocumentContentChangeEventWhole); ok {
			s.update(ctx, params.TextDocument.URI, params.TextDocument.Version, change.Text)
			return nil
		}
	}
	return nil
}

func (s *Server) didClose(ctx *glsp.Context, params *protocol.DidCloseTextDocumentParams) error {
	uri := params.TextDocument.URI
	path := key(uri)

	s.mu.Lock()
	delete(s.open, path)
	// The editor may have discarded unsaved changes, so go back to what's on
	// disk. If the file is gone (or was never saved), forget it.
	if !s.loadFromDisk(path) {
		s.remove(path)
	}
	s.mu.Unlock()

	// Clear diagnostics so they don't linger after the file is closed.
	ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: []protocol.Diagnostic{},
	})
	return nil
}

func (s *Server) documentSymbol(_ *glsp.Context, params *protocol.DocumentSymbolParams) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.docs[key(params.TextDocument.URI)]
	if !ok {
		return nil, nil
	}
	return analysis.Symbols(doc), nil
}

func (s *Server) definition(_ *glsp.Context, params *protocol.DefinitionParams) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := key(params.TextDocument.URI)
	doc, ok := s.docs[path]
	if !ok {
		return nil, nil
	}
	workspace := s.visibleDocs(path)
	return analysis.Definition(workspace, doc, params.Position), nil
}

// update re-parses an open document and publishes its diagnostics.
func (s *Server) update(ctx *glsp.Context, uri protocol.DocumentUri, version protocol.Integer, text string) {
	publishedVersion := protocol.UInteger(version)
	path := key(uri)

	s.mu.Lock()
	doc := analysis.Parse(s.parser, uri, version, text)
	s.replace(path, doc)
	s.open[path] = true
	diagnostics := analysis.SyntaxDiagnostics(doc)
	s.mu.Unlock()

	ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, protocol.PublishDiagnosticsParams{
		URI:         uri,
		Version:     &publishedVersion,
		Diagnostics: diagnostics,
	})
}

// visibleDocs returns the documents compiled together with the one at path,
// sorted by path: its projects' files, or every document if no project lists
// it. Callers must hold mu.
func (s *Server) visibleDocs(path string) []*analysis.Document {
	paths, ok := s.projects.Visible(path)
	if !ok {
		paths = slices.Collect(maps.Keys(s.docs))
	}
	slices.Sort(paths)

	docs := make([]*analysis.Document, 0, len(paths))
	for _, p := range paths {
		if doc, ok := s.docs[p]; ok {
			docs = append(docs, doc)
		}
	}
	return docs
}

// loadFromDisk parses the file at path, unless the editor has it open. It
// reports whether the file is now loaded. Callers must hold mu.
func (s *Server) loadFromDisk(path string) bool {
	if s.open[path] {
		return true
	}
	text, err := readFile(path)
	if err != nil {
		return false
	}
	s.replace(path, analysis.Parse(s.parser, pathToURI(path), 0, text))
	return true
}

// replace stores doc under path, freeing any document it replaces. Callers
// must hold mu.
func (s *Server) replace(path string, doc *analysis.Document) {
	s.remove(path)
	s.docs[path] = doc
}

// remove forgets the document at path. Callers must hold mu.
func (s *Server) remove(path string) {
	if old, ok := s.docs[path]; ok {
		old.Close()
		delete(s.docs, path)
	}
}
