package style

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestConforms(t *testing.T) {
	prefixes := Default().Naming.Prefixes
	tests := []struct {
		c    Case
		name string
		want bool
	}{
		{TitleCase, "Request", true},
		{TitleCase, "HTTPServer", true},
		{TitleCase, "Request2", true},
		{TitleCase, "request", false},
		{TitleCase, "Request_Count", false},
		{TitleCase, "tRequest", true},
		{TitleCase, "eResponse", true},
		{TitleCase, "xRequest", false},
		{TitleCase, "tc", false},
		{SnakeCase, "request_count", true},
		{SnakeCase, "count2", true},
		{SnakeCase, "_hidden", true},
		{SnakeCase, "requestCount", false},
		{SnakeCase, "Request", false},
		{SnakeCase, "request__count", false},
		{SnakeCase, "tcLookup", true},
		{ScreamingCase, "ENGLISH", true},
		{ScreamingCase, "NOT_FOUND", true},
		{ScreamingCase, "English", false},
		{CamelCase, "requestCount", true},
		{CamelCase, "request_count", false},
		{Off, "anything_GOES", true},
	}
	for _, tt := range tests {
		if got := tt.c.Conforms(tt.name, prefixes); got != tt.want {
			t.Errorf("%s.Conforms(%q) = %v, want %v", tt.c, tt.name, got, tt.want)
		}
	}
	if SnakeCase.Conforms("tcLookup", nil) {
		t.Error("tcLookup conforms to snake_case with no prefixes allowed")
	}
}

func TestConvert(t *testing.T) {
	tests := []struct {
		c          Case
		name, want string
	}{
		{TitleCase, "request_count", "RequestCount"},
		{TitleCase, "requestCount", "RequestCount"},
		{TitleCase, "REQUEST_COUNT", "RequestCount"},
		{TitleCase, "http_server", "HttpServer"},
		{SnakeCase, "requestCount", "request_count"},
		{SnakeCase, "RequestCount", "request_count"},
		{SnakeCase, "HTTPServer", "http_server"},
		{SnakeCase, "count2Down", "count2_down"},
		{SnakeCase, "_requestCount", "_request_count"},
		{ScreamingCase, "English", "ENGLISH"},
		{ScreamingCase, "notFound", "NOT_FOUND"},
		{CamelCase, "request_count", "requestCount"},
		{SnakeCase, "___", ""},
	}
	for _, tt := range tests {
		if got := tt.c.Convert(tt.name); got != tt.want {
			t.Errorf("%s.Convert(%q) = %q, want %q", tt.c, tt.name, got, tt.want)
		}
	}
}

func TestSuggest(t *testing.T) {
	prefixes := Default().Naming.Prefixes
	tests := []struct {
		c          Case
		name, want string
	}{
		{TitleCase, "eSpec_BankBalance_Init", "eSpecBankBalanceInit"},
		{TitleCase, "tcBad_Name", "tcBadName"},
		{TitleCase, "lookup_done", "LookupDone"},
		{TitleCase, "eventHandler", "EventHandler"},
		{SnakeCase, "tcLookup_All", "tcLookupAll"},
		{SnakeCase, "requestCount", "request_count"},
		{SnakeCase, "___", ""},
	}
	for _, tt := range tests {
		if got := tt.c.Suggest(tt.name, prefixes); got != tt.want {
			t.Errorf("%s.Suggest(%q) = %q, want %q", tt.c, tt.name, got, tt.want)
		}
	}
}

func TestWords(t *testing.T) {
	if got, want := Words("HTTPServer_v2Ready"), []string{"HTTP", "Server", "v2", "Ready"}; !slices.Equal(got, want) {
		t.Errorf("Words = %q, want %q", got, want)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("[naming]\ntest_cases = \"TitleCase\"\nprefixes = []\n")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Naming.TestCases != TitleCase || len(c.Naming.Prefixes) != 0 || c.Naming.Events != TitleCase {
		t.Errorf("Load = %+v", c.Naming)
	}

	write("[naming]\nevents = \"kebab-case\"\n")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "kebab-case") {
		t.Errorf("unknown case: err = %v", err)
	}
	write("[naming]\nevent = \"TitleCase\"\n")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "naming.event") {
		t.Errorf("unknown setting: err = %v", err)
	}

	sub := filepath.Join(dir, "PSrc", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Find(sub); got != path {
		t.Errorf("Find = %q, want %q", got, path)
	}
}
