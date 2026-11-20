package diff

import (
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnified(t *testing.T) {
	a := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\n"
	b := "a\nB\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\n"
	want := `--- old
+++ new
@@ -1,5 +1,5 @@
 a
-b
+B
 c
 d
 e
@@ -9,3 +9,4 @@
 i
 j
 k
+l
`
	if got := string(Unified("old", "new", []byte(a), []byte(b))); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if Unified("old", "new", []byte(a), []byte(a)) != nil {
		t.Error("a diff of equal text")
	}
}

func TestMissingFinalNewline(t *testing.T) {
	got := string(Unified("old", "new", []byte("a\nb"), []byte("a\nb\n")))
	want := "--- old\n+++ new\n@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+b\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// TestAgainstPatch checks random diffs by applying them with patch(1),
// when it's installed.
func TestAgainstPatch(t *testing.T) {
	if _, err := exec.LookPath("patch"); err != nil {
		t.Skip("patch isn't installed")
	}
	r := rand.New(rand.NewSource(1))
	words := []string{"a\n", "b\n", "c\n", "d\n", "\n"}
	random := func() string {
		var lines []string
		for range r.Intn(30) {
			lines = append(lines, words[r.Intn(len(words))])
		}
		return strings.Join(lines, "")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	for i := range 300 {
		a, b := random(), random()
		os.WriteFile(file, []byte(a), 0o644)
		d := Unified("f", "f", []byte(a), []byte(b))
		if d == nil {
			continue
		}
		cmd := exec.Command("patch", "-s", "-u", file)
		cmd.Stdin = strings.NewReader(string(d))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("case %d: patch failed: %v\n%s\ndiff:\n%s", i, err, out, d)
		}
		if got, _ := os.ReadFile(file); string(got) != b {
			t.Fatalf("case %d: patched\n%q\nwant\n%q\ndiff:\n%s", i, got, b, d)
		}
	}
}
