// Package server wires the analysis package to the language server protocol.
package server

import (
	"sync"

	"github.com/bencornia/p-lsp/internal/analysis"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	glspserver "github.com/tliron/glsp/server"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

const name = "p-lsp"

type Server struct {
	version string
	handler protocol.Handler

	// mu guards parser and docs. glsp currently handles messages one at a
	// time, but nothing in its API promises that.
	mu     sync.Mutex
	parser *sitter.Parser
	docs   map[protocol.DocumentUri]*analysis.Document
}

func New(version string) *Server {
	s := &Server{
		version: version,
		parser:  analysis.NewParser(),
		docs:    map[protocol.DocumentUri]*analysis.Document{},
	}
	s.handler = protocol.Handler{
		Initialize:                 s.initialize,
		Shutdown:                   s.shutdown,
		TextDocumentDidOpen:        s.didOpen,
		TextDocumentDidChange:      s.didChange,
		TextDocumentDidClose:       s.didClose,
		TextDocumentDocumentSymbol: s.documentSymbol,
	}
	return s
}

// RunStdio serves LSP over stdin/stdout until the client disconnects.
func (s *Server) RunStdio() error {
	return glspserver.NewServer(&s.handler, name, false).RunStdio()
}

func (s *Server) initialize(_ *glsp.Context, _ *protocol.InitializeParams) (any, error) {
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
	for uri, doc := range s.docs {
		doc.Close()
		delete(s.docs, uri)
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

	s.mu.Lock()
	if doc, ok := s.docs[uri]; ok {
		doc.Close()
		delete(s.docs, uri)
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
	doc, ok := s.docs[params.TextDocument.URI]
	if !ok {
		return nil, nil
	}
	return analysis.Symbols(doc), nil
}

// update re-parses a document and publishes its diagnostics.
func (s *Server) update(ctx *glsp.Context, uri protocol.DocumentUri, version protocol.Integer, text string) {
	publishedVersion := protocol.UInteger(version)
	s.mu.Lock()
	doc := analysis.Parse(s.parser, uri, version, text)
	if old, ok := s.docs[uri]; ok {
		old.Close()
	}
	s.docs[uri] = doc
	diagnostics := analysis.SyntaxDiagnostics(doc)
	s.mu.Unlock()

	ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, protocol.PublishDiagnosticsParams{
		URI:         uri,
		Version:     &publishedVersion,
		Diagnostics: diagnostics,
	})
}
