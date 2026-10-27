// Package server wires the analysis package to the language server protocol.
package server

import (
	"maps"
	"path/filepath"
	"slices"
	"sync"

	"github.com/bencornia/p-lsp/internal/analysis"
	"github.com/bencornia/p-lsp/internal/compile"
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
	// discovered are .pproj files found by searching upward from files
	// opened outside the workspace; see discoverProject.
	discovered []string
	// watch describes what the client can watch for us.
	watch watchSupport
	// watchers are the file watchers last sent to registerWatchers.
	watchers []fileSystemWatcher
	// pendingWatchers holds the latest watchers for registerWatchers to
	// register; see queueWatchers.
	pendingWatchers chan []fileSystemWatcher

	// compiler type-checks projects on save; nil when p isn't available.
	compiler *compile.Runner
	// compileDiagnostics holds each project's last type-check diagnostics,
	// by project and then by file path.
	compileDiagnostics map[string]map[string][]protocol.Diagnostic
}

func New(version string) *Server {
	s := &Server{
		version:         version,
		parser:          analysis.NewParser(),
		docs:            map[string]*analysis.Document{},
		open:            map[string]bool{},
		projects:        project.NewIndex(),
		pendingWatchers: make(chan []fileSystemWatcher, 1),
	}
	s.handler = protocol.Handler{
		Initialize:                     s.initialize,
		Initialized:                    s.initialized,
		Shutdown:                       s.shutdown,
		TextDocumentDidOpen:            s.didOpen,
		TextDocumentDidChange:          s.didChange,
		TextDocumentDidClose:           s.didClose,
		TextDocumentDidSave:            s.didSave,
		TextDocumentDocumentSymbol:     s.documentSymbol,
		TextDocumentDefinition:         s.definition,
		TextDocumentReferences:         s.references,
		TextDocumentHover:              s.hover,
		TextDocumentCompletion:         s.completion,
		TextDocumentPrepareRename:      s.prepareRename,
		TextDocumentRename:             s.rename,
		WorkspaceSymbol:                s.workspaceSymbol,
		WorkspaceDidChangeWatchedFiles: s.didChangeWatchedFiles,
	}
	return s
}

// RunStdio serves LSP over stdin/stdout until the client disconnects.
func (s *Server) RunStdio() error {
	return glspserver.NewServer(&s.handler, name, false).RunStdio()
}

func (s *Server) initialize(ctx *glsp.Context, params *protocol.InitializeParams) (any, error) {
	var roots []string
	for _, folder := range params.WorkspaceFolders {
		roots = append(roots, uriToPath(folder.URI))
	}
	if len(roots) == 0 && params.RootURI != nil {
		roots = append(roots, uriToPath(*params.RootURI))
	}

	s.mu.Lock()
	s.roots = roots
	s.watch = parseWatchSupport(ctx.Params)
	s.reloadWorkspace()
	s.mu.Unlock()

	capabilities := s.handler.CreateServerCapabilities()

	// Re-parsing a whole P file takes well under a millisecond, so ask for
	// full text on every change rather than applying incremental edits.
	full := protocol.TextDocumentSyncKindFull
	capabilities.TextDocumentSync.(*protocol.TextDocumentSyncOptions).Change = &full

	// glsp advertises plain renameProvider: true; say we also check whether
	// a name can be renamed before the client asks for the new name.
	prepare := true
	capabilities.RenameProvider = protocol.RenameOptions{PrepareProvider: &prepare}

	// Complete fields as soon as a `.` is typed.
	capabilities.CompletionProvider.TriggerCharacters = []string{"."}

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

	s.mu.Lock()
	s.discoverProject(key(item.URI))
	s.mu.Unlock()
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
	// Syntax errors go with the editor's copy; type errors from the last
	// compile stay until the next one.
	diagnostics := s.diagnosticsFor(path)
	s.mu.Unlock()

	ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnostics,
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

func (s *Server) hover(_ *glsp.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := key(params.TextDocument.URI)
	doc, ok := s.docs[path]
	if !ok {
		return nil, nil
	}
	return analysis.Hover(s.visibleDocs(path), doc, params.Position), nil
}

func (s *Server) completion(_ *glsp.Context, params *protocol.CompletionParams) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := key(params.TextDocument.URI)
	doc, ok := s.docs[path]
	if !ok {
		return nil, nil
	}
	return analysis.Completion(s.parser, s.visibleDocs(path), doc, params.Position), nil
}

// maxWorkspaceSymbols caps workspace symbol results; editors re-query as the
// user types, so the best matches are what matter.
const maxWorkspaceSymbols = 500

func (s *Server) workspaceSymbol(_ *glsp.Context, params *protocol.WorkspaceSymbolParams) ([]protocol.SymbolInformation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return analysis.WorkspaceSymbols(s.allDocs(), params.Query, maxWorkspaceSymbols), nil
}

func (s *Server) prepareRename(_ *glsp.Context, params *protocol.PrepareRenameParams) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := key(params.TextDocument.URI)
	doc, ok := s.docs[path]
	if !ok {
		return nil, nil
	}
	if rng := analysis.PrepareRename(s.visibleDocs(path), doc, params.Position); rng != nil {
		return rng, nil
	}
	return nil, nil
}

func (s *Server) rename(_ *glsp.Context, params *protocol.RenameParams) (*protocol.WorkspaceEdit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.docs[key(params.TextDocument.URI)]
	if !ok {
		return nil, nil
	}
	changes, err := analysis.Rename(s.allDocs(), s.workspaceOf(), doc, params.Position, params.NewName)
	if err != nil {
		// The client shows this message to the user.
		return nil, err
	}
	return &protocol.WorkspaceEdit{Changes: changes}, nil
}

// workspaceOf returns a function giving the documents each document is
// resolved against, which are the same for every identifier in it, so
// they're cached for the length of one request. Callers must hold mu while
// using it.
func (s *Server) workspaceOf() func(*analysis.Document) []*analysis.Document {
	cache := map[*analysis.Document][]*analysis.Document{}
	return func(d *analysis.Document) []*analysis.Document {
		if w, ok := cache[d]; ok {
			return w
		}
		w := s.visibleDocs(key(d.URI))
		cache[d] = w
		return w
	}
}

func (s *Server) references(_ *glsp.Context, params *protocol.ReferenceParams) ([]protocol.Location, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := key(params.TextDocument.URI)
	doc, ok := s.docs[path]
	if !ok {
		return nil, nil
	}
	return analysis.References(s.allDocs(), s.workspaceOf(), doc, params.Position, params.Context.IncludeDeclaration), nil
}

// update re-parses an open document and publishes its diagnostics.
func (s *Server) update(ctx *glsp.Context, uri protocol.DocumentUri, version protocol.Integer, text string) {
	publishedVersion := protocol.UInteger(version)
	path := key(uri)

	s.mu.Lock()
	doc := analysis.Parse(s.parser, uri, version, text)
	s.replace(path, doc)
	s.open[path] = true
	diagnostics := s.diagnosticsFor(path)
	s.mu.Unlock()

	ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, protocol.PublishDiagnosticsParams{
		URI:         uri,
		Version:     &publishedVersion,
		Diagnostics: diagnostics,
	})
}

// visibleDocs returns the documents compiled together with the one at path,
// sorted by path: its projects' files, or for a file no project lists, the
// files under its project.FallbackRoot. Callers must hold mu.
func (s *Server) visibleDocs(path string) []*analysis.Document {
	if paths, ok := s.projects.Visible(path); ok {
		return s.docsAt(paths)
	}
	if !filepath.IsAbs(path) {
		// An unsaved buffer, such as untitled:Untitled-1, has no directory.
		return s.allDocs()
	}
	root := project.FallbackRoot(path)
	var paths []string
	for p := range s.docs {
		if project.Within(p, root) {
			paths = append(paths, p)
		}
	}
	return s.docsAt(paths)
}

// allDocs returns every document, sorted by path. Callers must hold mu.
func (s *Server) allDocs() []*analysis.Document {
	return s.docsAt(slices.Collect(maps.Keys(s.docs)))
}

// docsAt returns the loaded documents at paths, sorted by path. Callers must
// hold mu.
func (s *Server) docsAt(paths []string) []*analysis.Document {
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
