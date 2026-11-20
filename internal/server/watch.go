package server

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/bencornia/ptools/internal/project"
	"github.com/bencornia/ptools/internal/style"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// glsp implements LSP 3.16, where a file watcher's glob is a string matched
// within the workspace. LSP 3.17 also allows a RelativePattern, which names
// its own base directory and is the only way to watch outside the workspace.
// These types send either form.

type fileSystemWatcher struct {
	// GlobPattern is a string or a relativePattern.
	GlobPattern any `json:"globPattern"`
}

type relativePattern struct {
	BaseURI string `json:"baseUri"`
	Pattern string `json:"pattern"`
}

type watchRegistrationOptions struct {
	Watchers []fileSystemWatcher `json:"watchers"`
}

// watchSupport is what the client can watch for us.
type watchSupport struct {
	// dynamic is whether we can register file watchers at all.
	dynamic bool
	// relative is whether watchers can use RelativePatterns (LSP 3.17).
	relative bool
}

// parseWatchSupport reads the client's file watching capabilities from the
// raw initialize params, since glsp's types predate relativePatternSupport.
func parseWatchSupport(params json.RawMessage) watchSupport {
	var raw struct {
		Capabilities struct {
			Workspace struct {
				DidChangeWatchedFiles struct {
					DynamicRegistration    bool `json:"dynamicRegistration"`
					RelativePatternSupport bool `json:"relativePatternSupport"`
				} `json:"didChangeWatchedFiles"`
			} `json:"workspace"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(params, &raw); err != nil {
		return watchSupport{}
	}
	watch := raw.Capabilities.Workspace.DidChangeWatchedFiles
	return watchSupport{dynamic: watch.DynamicRegistration, relative: watch.RelativePatternSupport}
}

const watchRegistrationID = "p-files"

// initialized starts keeping the client's file watchers in step with the
// workspace, so new files and project changes are picked up without a restart.
func (s *Server) initialized(ctx *glsp.Context, _ *protocol.InitializedParams) error {
	s.mu.Lock()
	watch := s.watch
	s.mu.Unlock()
	if !watch.dynamic {
		log.Infof("client can't watch files; restart the server to pick up new files and .pproj changes")
		return nil
	}
	// glsp handles one message at a time, so waiting in this handler for the
	// client's replies would deadlock.
	go s.registerWatchers(ctx)
	return nil
}

// registerWatchers registers each set of watchers queued by updateWatchers,
// replacing the previous set. It runs for the life of the connection.
func (s *Server) registerWatchers(ctx *glsp.Context) {
	registered := false
	for watchers := range s.pendingWatchers {
		if registered {
			ctx.Call(string(protocol.ServerClientUnregisterCapability), protocol.UnregistrationParams{
				Unregisterations: []protocol.Unregistration{{
					ID:     watchRegistrationID,
					Method: string(protocol.MethodWorkspaceDidChangeWatchedFiles),
				}},
			}, nil)
		}
		ctx.Call(string(protocol.ServerClientRegisterCapability), protocol.RegistrationParams{
			Registrations: []protocol.Registration{{
				ID:              watchRegistrationID,
				Method:          string(protocol.MethodWorkspaceDidChangeWatchedFiles),
				RegisterOptions: watchRegistrationOptions{Watchers: watchers},
			}},
		}, nil)
		registered = true
	}
}

// updateWatchers queues a new set of watchers for registerWatchers if the
// directories to watch have changed. Callers must hold mu.
func (s *Server) updateWatchers() {
	if !s.watch.dynamic {
		return
	}
	watchers := []fileSystemWatcher{{GlobPattern: "**/*.p"}, {GlobPattern: "**/*.pproj"}, {GlobPattern: "**/" + style.FileName}}
	external := s.externalProjectDirs()
	if s.watch.relative {
		for _, dir := range external {
			uri := pathToURI(dir)
			watchers = append(watchers,
				fileSystemWatcher{GlobPattern: relativePattern{BaseURI: uri, Pattern: "**/*.p"}},
				fileSystemWatcher{GlobPattern: relativePattern{BaseURI: uri, Pattern: "**/*.pproj"}},
			)
		}
	}
	if reflect.DeepEqual(watchers, s.watchers) {
		return
	}
	if !s.watch.relative && len(external) > 0 {
		log.Infof("client can't watch outside the workspace; restart the server to pick up changes in %s",
			strings.Join(external, ", "))
	}
	s.watchers = watchers
	s.queueWatchers(watchers)
}

// queueWatchers hands watchers to registerWatchers, replacing any set it
// hasn't picked up yet, since only the latest matters.
func (s *Server) queueWatchers(watchers []fileSystemWatcher) {
	for {
		select {
		case s.pendingWatchers <- watchers:
			return
		default:
			select {
			case <-s.pendingWatchers:
			default:
			}
		}
	}
}

// externalProjectDirs returns the directories of loaded projects that lie
// outside every workspace root, such as the P tutorials' ../Common/Timer.
// Callers must hold mu.
func (s *Server) externalProjectDirs() []string {
	var dirs []string
	for _, pproj := range s.projects.Projects() {
		dir := filepath.Dir(pproj)
		if !s.inWorkspace(dir) && (len(dirs) == 0 || dirs[len(dirs)-1] != dir) {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func (s *Server) inWorkspace(path string) bool {
	for _, root := range s.roots {
		if project.Within(path, root) {
			return true
		}
	}
	return false
}
