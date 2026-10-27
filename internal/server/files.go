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
	return filepath.Clean(filepath.FromSlash(u.Path))
}

func pathToURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func readFile(path string) (string, error) {
	text, err := os.ReadFile(path)
	return string(text), err
}
