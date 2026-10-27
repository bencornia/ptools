package server

import (
	"path/filepath"
	"strings"

	"github.com/bencornia/p-lsp/internal/project"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// initialized asks the client to tell us when P files or projects change on
// disk, so the workspace stays current without a restart.
func (s *Server) initialized(ctx *glsp.Context, _ *protocol.InitializedParams) error {
	s.mu.Lock()
	watchFiles := s.watchFiles
	s.mu.Unlock()
	if !watchFiles {
		log.Infof("client can't watch files; restart the server to pick up new files and .pproj changes")
		return nil
	}

	params := protocol.RegistrationParams{Registrations: []protocol.Registration{{
		ID:     "p-files",
		Method: string(protocol.MethodWorkspaceDidChangeWatchedFiles),
		RegisterOptions: protocol.DidChangeWatchedFilesRegistrationOptions{
			Watchers: []protocol.FileSystemWatcher{
				{GlobPattern: "**/*.p"},
				{GlobPattern: "**/*.pproj"},
			},
		},
	}}}
	// glsp handles one message at a time, so waiting here for the client's
	// reply would deadlock.
	go ctx.Call(string(protocol.ServerClientRegisterCapability), params, nil)
	return nil
}

func (s *Server) didChangeWatchedFiles(_ *glsp.Context, params *protocol.DidChangeWatchedFilesParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	reload := false
	for _, change := range params.Changes {
		path := key(change.URI)
		switch {
		case strings.EqualFold(filepath.Ext(path), ".pproj"):
			reload = true
		case change.Type == protocol.FileChangeTypeChanged:
			// Open files follow the editor's text, which loadFromDisk
			// leaves alone.
			if _, loaded := s.docs[path]; loaded {
				s.loadFromDisk(path)
			}
		default:
			// A created or deleted .p file can change which files a
			// project's <PFile> directories include.
			reload = true
		}
	}
	if reload {
		s.reloadWorkspace()
	}
	return nil
}

// reloadWorkspace rebuilds the project index from the .pproj files on disk and
// loads or drops documents to match: every P file under the workspace roots,
// plus every file in the projects they include. Open documents are kept.
// Callers must hold mu.
func (s *Server) reloadWorkspace() {
	projects := project.NewIndex()
	want := map[string]bool{}
	for _, root := range s.roots {
		pFiles, pprojs, err := project.Find(root)
		if err != nil {
			log.Errorf("loading workspace %s: %s", root, err)
		}
		projects.Load(pprojs...)
		for _, path := range pFiles {
			want[path] = true
		}
	}
	// Included projects can live outside the workspace, as with the P
	// tutorials' ../Common/Timer.
	for _, path := range projects.Files() {
		want[path] = true
	}
	for path, err := range projects.Errors {
		log.Errorf("reading project %s: %s", path, err)
	}
	s.projects = projects

	for path := range s.docs {
		if !want[path] && !s.open[path] {
			s.remove(path)
		}
	}
	for path := range want {
		if _, loaded := s.docs[path]; !loaded {
			s.loadFromDisk(path)
		}
	}
	log.Infof("workspace has %d P files", len(s.docs))
}
