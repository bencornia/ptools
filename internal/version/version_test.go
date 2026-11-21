package version

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestGrammar(t *testing.T) {
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`github\.com/bencornia/tree-sitter-p v\S+-([0-9a-f]{12})\b`).FindSubmatch(mod)
	if m == nil {
		t.Fatal("go.mod doesn't require tree-sitter-p at a pseudo-version")
	}
	if !strings.HasPrefix(Grammar, string(m[1])) {
		t.Errorf("Grammar is %s, but go.mod requires tree-sitter-p %s; set it to that commit's full hash", Grammar, m[1])
	}
}
