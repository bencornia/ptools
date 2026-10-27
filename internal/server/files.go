package server

import (
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// key identifies a document. The editor and the server can spell the same
// file's URI differently (percent-encoding, for one), so file URIs are keyed
// by their cleaned path. Other schemes, such as untitled:, are keyed as is.
func key(uri string) string {
	return uriToPath(uri)
}

func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return uri
	}
	return filepath.Clean(filepath.FromSlash(u.Path))
}

func pathToURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

// findPFiles returns the .p files under root, skipping hidden directories and
// macOS "._" metadata files.
func findPFiles(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable directories rather than abandoning the walk.
			if entry != nil && entry.IsDir() && path != root {
				return fs.SkipDir
			}
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			if path != root && strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(name) == ".p" && !strings.HasPrefix(name, "._") {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

func readFile(path string) (string, error) {
	text, err := os.ReadFile(path)
	return string(text), err
}
