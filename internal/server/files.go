package server

import (
	"net/url"
	"os"
	"path/filepath"
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
	path := u.Path
	switch {
	case u.Opaque != "":
		// file:relative/path
		path = u.Opaque
	case u.Host == "." || u.Host == "..":
		// file://. or file://../dir, a relative path whose first
		// element parses as the host.
		path = u.Host + u.Path
	}
	path = filepath.FromSlash(path)
	// Relative paths would key the same file two ways: as found under a
	// relative workspace root, and as listed by its absolute .pproj.
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			return abs
		}
	}
	return filepath.Clean(path)
}

func pathToURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func readFile(path string) (string, error) {
	text, err := os.ReadFile(path)
	return string(text), err
}
