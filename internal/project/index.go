package project

import (
	"slices"
	"sort"
)

// Index holds a set of projects and the projects they include.
type Index struct {
	projects map[string]*Project
	// owners maps a .p file to the projects that list it directly.
	owners map[string][]*Project
	// Errors holds the projects that couldn't be read, keyed by path.
	Errors map[string]error
}

func NewIndex() *Index {
	return &Index{
		projects: map[string]*Project{},
		owners:   map[string][]*Project{},
		Errors:   map[string]error{},
	}
}

// Load parses the .pproj files at paths and, transitively, the projects they
// include. Projects that can't be read are recorded in Errors and skipped.
func (x *Index) Load(paths ...string) {
	for _, path := range paths {
		if _, seen := x.projects[path]; seen || x.Errors[path] != nil {
			continue
		}
		p, err := Parse(path)
		if err != nil {
			x.Errors[path] = err
			continue
		}
		x.projects[p.Path] = p
		for _, file := range p.Files {
			x.owners[file] = append(x.owners[file], p)
		}
		x.Load(p.Includes...)
	}
}

// Projects returns the paths of every loaded .pproj file, sorted.
func (x *Index) Projects() []string {
	paths := make([]string, 0, len(x.projects))
	for path := range x.projects {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// Project returns the loaded project whose .pproj file is path.
func (x *Index) Project(path string) (*Project, bool) {
	p, ok := x.projects[path]
	return p, ok
}

// Owners returns the .pproj files that list file directly, sorted.
func (x *Index) Owners(file string) []string {
	var paths []string
	for _, p := range x.owners[file] {
		paths = append(paths, p.Path)
	}
	sort.Strings(paths)
	return paths
}

// Files returns every .p file in every loaded project, sorted.
func (x *Index) Files() []string {
	files := make([]string, 0, len(x.owners))
	for file := range x.owners {
		files = append(files, file)
	}
	sort.Strings(files)
	return files
}

// Visible returns the files compiled together with file: everything in the
// projects that list it, and in the projects those include. A file shared
// through <IncludeProject> sees only its own project, not every project that
// includes it. ok is false if no loaded project lists file.
func (x *Index) Visible(file string) (files []string, ok bool) {
	owners := x.owners[file]
	if len(owners) == 0 {
		return nil, false
	}
	return x.files(owners), true
}

// Users returns the files that can use what file declares: those compiled
// with it, and those compiled with every project that includes one of its
// projects, directly or through others, as the projects using a library
// include it. ok is false if no loaded project lists file.
func (x *Index) Users(file string) (files []string, ok bool) {
	users := slices.Clone(x.owners[file])
	if len(users) == 0 {
		return nil, false
	}
	seen := map[string]bool{}
	for _, p := range users {
		seen[p.Path] = true
	}
	for i := 0; i < len(users); i++ {
		for _, q := range x.projects {
			if !seen[q.Path] && slices.Contains(q.Includes, users[i].Path) {
				seen[q.Path] = true
				users = append(users, q)
			}
		}
	}
	return x.files(users), true
}

// files returns the files of projects and of the projects they include,
// sorted.
func (x *Index) files(projects []*Project) []string {
	var files []string
	seen := map[string]bool{}
	visited := map[string]bool{}
	var visit func(p *Project)
	visit = func(p *Project) {
		if visited[p.Path] {
			return
		}
		visited[p.Path] = true
		for _, f := range p.Files {
			if !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
		for _, include := range p.Includes {
			if q, ok := x.projects[include]; ok {
				visit(q)
			}
		}
	}
	for _, p := range projects {
		visit(p)
	}
	sort.Strings(files)
	return files
}
