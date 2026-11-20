package server

import (
	"path/filepath"
	"strings"

	"github.com/bencornia/ptools/internal/project"
	"github.com/bencornia/ptools/internal/style"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func (s *Server) didChangeWatchedFiles(ctx *glsp.Context, params *protocol.DidChangeWatchedFilesParams) error {
	s.mu.Lock()
	reload, restyle := false, false
	for _, change := range params.Changes {
		path := key(change.URI)
		switch {
		case filepath.Base(path) == style.FileName:
			restyle = true
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
	var notifications []protocol.PublishDiagnosticsParams
	if restyle {
		notifications = s.openDiagnostics()
	}
	s.mu.Unlock()

	for _, n := range notifications {
		ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, n)
	}
	return nil
}

// reloadWorkspace rebuilds the project index from the .pproj files on disk and
// loads or drops documents to match: every P file under the workspace roots,
// plus every file in the projects they include or that were discovered.
// Open documents are kept. Callers must hold mu.
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
	projects.Load(s.discovered...)
	// Included and discovered projects can live outside the workspace, as
	// with the P tutorials' ../Common/Timer.
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

	// The projects may now include different directories outside the
	// workspace.
	s.updateWatchers()
}

// discoverProject finds the project of a file opened outside the workspace,
// whose .pproj wasn't found when the workspace was loaded. It searches the
// file's directory and then each parent for .pproj files, and keeps the first
// that list the file, for the rest of the session. Callers must hold mu.
func (s *Server) discoverProject(path string) {
	if !filepath.IsAbs(path) || s.inWorkspace(path) {
		return
	}
	if _, ok := s.projects.Visible(path); ok {
		return
	}

	if found := project.Discover(path); len(found) > 0 {
		log.Infof("found %s for %s", strings.Join(found, ", "), path)
		s.discovered = append(s.discovered, found...)
		s.reloadWorkspace()
	}
}
