// Package project reads P project (.pproj) files to work out which source
// files are compiled together, following the P compiler's rules.
package project

import (
	"encoding/xml"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Project is a parsed .pproj file. All paths are absolute and cleaned.
type Project struct {
	// Path is the .pproj file.
	Path string
	// Files are the .p files the project lists in <InputFiles>, with
	// directories expanded.
	Files []string
	// Includes are the .pproj files named by <IncludeProject>.
	Includes []string
}

type pprojXML struct {
	InputFiles []struct {
		PFiles []string `xml:"PFile"`
	} `xml:"InputFiles"`
	IncludeProjects []string `xml:"IncludeProject"`
}

// Parse reads the .pproj file at path.
func Parse(path string) (*Project, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var parsed pprojXML
	if err := xml.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}

	dir := filepath.Dir(path)
	p := &Project{Path: path}
	for _, inputs := range parsed.InputFiles {
		for _, entry := range inputs.PFiles {
			p.Files = append(p.Files, expand(resolvePath(dir, entry))...)
		}
	}
	for _, include := range parsed.IncludeProjects {
		// The compiler resolves includes against its working directory,
		// which is normally the including project's directory.
		p.Includes = append(p.Includes, resolvePath(dir, include))
	}
	return p, nil
}

// resolvePath resolves a path from a .pproj file against dir. Projects
// written on Windows may use backslashes.
func resolvePath(dir, path string) string {
	path = filepath.FromSlash(strings.ReplaceAll(strings.TrimSpace(path), `\`, "/"))
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return filepath.Clean(path)
}

// expand returns the .p files a <PFile> entry names: every .p file under it
// if it's a directory, or the file itself if it's an existing .p file.
func expand(path string) []string {
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return nil
	case info.IsDir():
		files, _, _ := Find(path)
		return files
	case isPFile(path):
		return []string{path}
	}
	return nil
}

// Find returns the .p and .pproj files under root, skipping hidden files and
// directories as the P compiler does. That also skips macOS "._" files.
func Find(root string) (pFiles, projects []string, err error) {
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable directories rather than abandoning the walk.
			if entry != nil && entry.IsDir() && path != root {
				return fs.SkipDir
			}
			return err
		}
		hidden := strings.HasPrefix(entry.Name(), ".")
		switch {
		case entry.IsDir():
			if hidden && path != root {
				return fs.SkipDir
			}
		case hidden:
		case isPFile(path):
			pFiles = append(pFiles, filepath.Clean(path))
		case strings.EqualFold(filepath.Ext(path), ".pproj"):
			projects = append(projects, filepath.Clean(path))
		}
		return nil
	})
	return pFiles, projects, err
}

func isPFile(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".p")
}
