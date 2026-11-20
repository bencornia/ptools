package format

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSource(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"indents blocks",
			"machine M {\nstart state S {\nentry {\nx = 1;\n}\n}\n}\n",
			"machine M {\n  start state S {\n    entry {\n      x = 1;\n    }\n  }\n}\n"},
		{"re-indents", "fun F() {\n        x = 1;\n\t\ty = 2;\n}\n", "fun F() {\n  x = 1;\n  y = 2;\n}\n"},
		{"brace on its own line", "machine M\n{\n  var x: int;\n}\n", "machine M\n{\n  var x: int;\n}\n"},
		{"spaces around operators", "fun F() {\n  x=a+b*-c;\n  y += (1);\n  b = !(x==y)&&z;\n}\n",
			"fun F() {\n  x = a + b * -c;\n  y += (1);\n  b = !(x == y) && z;\n}\n"},
		{"type annotations", "machine M {\n  var x : int;\n}\nfun F(a:int, b : seq[int]) : map[int,bool] {}\n",
			"machine M {\n  var x: int;\n}\nfun F(a: int, b: seq[int]): map[int, bool] {}\n"},
		{"keywords before parentheses", "fun F() {\n  if(x) {}\n  while(y) {}\n  x = sizeof (s) + keys (m)[0];\n}\n",
			"fun F() {\n  if (x) {}\n  while (y) {}\n  x = sizeof(s) + keys(m)[0];\n}\n"},
		{"calls and constructors", "fun F() {\n  G (1,2);\n  m = new M ((a = 1,));\n}\n",
			"fun F() {\n  G(1, 2);\n  m = new M((a = 1,));\n}\n"},
		{"handlers", "machine M {\n  start state S {\n    entry(x: int){}\n    on e do (p: T){}\n  }\n}\n",
			"machine M {\n  start state S {\n    entry (x: int) {}\n    on e do (p: T) {}\n  }\n}\n"},
		{"one-line blocks", "module C = {Client};\nfun F() { return 1; }\n", "module C = { Client };\nfun F() { return 1; }\n"},
		{"brace-less if", "fun F() {\n  if (x)\n  raise halt;\n  else\n  y = 1;\n  z = 2;\n}\n",
			"fun F() {\n  if (x)\n    raise halt;\n  else\n    y = 1;\n  z = 2;\n}\n"},
		{"continued statement", "fun F() {\n  assert x in s,\n  format(\"{0}\",\n  x);\n}\n",
			"fun F() {\n  assert x in s,\n    format(\"{0}\",\n      x);\n}\n"},
		{"closing bracket lines up", "fun F() {\n  G(\n      1,\n      2\n      );\n}\n", "fun F() {\n  G(\n    1,\n    2\n  );\n}\n"},
		{"quantifier", "invariant i: forall (c: C)::c.x;\n", "invariant i: forall (c: C) :: c.x;\n"},
		{"test declaration", "test t [main=M]:\nassert S in\n(union A, { M });\n",
			"test t [main=M]:\n  assert S in\n  (union A, { M });\n"},
		{"parameter list", "test param (n in [2,3]) t [main=M]: (union A, B);\n", "test param (n in [2, 3]) t [main=M]: (union A, B);\n"},
		{"floats and fields", "fun F() {\n  x = 1.5 + t.0 + r . f;\n}\n", "fun F() {\n  x = 1.5 + t.0 + r.f;\n}\n"},
		{"comments stay", "// top\n\n\n\nevent e;   // trailing\n/* block\n   comment */\nfun F() {\n    // inside\n  x = 1;\n}\n",
			"// top\n\nevent e; // trailing\n/* block\n   comment */\nfun F() {\n  // inside\n  x = 1;\n}\n"},
		{"trailing whitespace and final newline", "event e;   \n\n\nevent f;", "event e;\n\nevent f;\n"},
		{"blank lines between states",
			"machine M {\n  var x: int;\n  start state A {\n    entry {\n    }\n    exit {\n    }\n  }\n  // B waits.\n  state B { }\n  state C { entry f; exit f; }\n  fun f() { }\n}\n",
			"machine M {\n  var x: int;\n  start state A {\n    entry {\n    }\n\n    exit {\n    }\n  }\n\n  // B waits.\n  state B {}\n\n  state C { entry f; exit f; }\n  fun f() {}\n}\n"},
		{"one-line handlers stay together",
			"machine M {\n  start state A {\n    entry f;\n    on e goto A;\n    defer d;\n    ignore i;\n    exit f;\n  }\n}\n",
			"machine M {\n  start state A {\n    entry f;\n    on e goto A;\n    defer d;\n    ignore i;\n    exit f;\n  }\n}\n"},
		{"blank lines around multi-line handlers",
			"machine M {\n  start state A {\n    entry {\n      x = 1;\n    }\n    on e do {\n      x = 2;\n    }\n    // f goes back.\n    on f goto A;\n    ignore i;\n    on g do (p: int) {\n    }\n  }\n}\n",
			"machine M {\n  start state A {\n    entry {\n      x = 1;\n    }\n\n    on e do {\n      x = 2;\n    }\n\n    // f goes back.\n    on f goto A;\n    ignore i;\n\n    on g do (p: int) {\n    }\n  }\n}\n"},
		{"no blank lines at the edges of blocks",
			"machine M {\n\n  start state Done {\n    entry {\n\n      x = 1;\n      // last\n\n    }\n\n  }\n\n}\n",
			"machine M {\n  start state Done {\n    entry {\n      x = 1;\n      // last\n    }\n  }\n}\n"},
		{"aligned trailing comments",
			"machine Main { // <.>\n  start state Init {    // <.>\n    entry {\n      print \"hello\"; // <.>\n      x = 1;  /* inline */ y = 2; // last\n      z = \"é\"; // runes\n\n      w = 1; // alone\n    }\n  }\n}\n",
			"machine Main {       // <.>\n  start state Init { // <.>\n    entry {\n      print \"hello\";             // <.>\n      x = 1; /* inline */ y = 2; // last\n      z = \"é\";                   // runes\n\n      w = 1; // alone\n    }\n  }\n}\n"},
		{"leading blank lines", "\n\nevent e;\n", "event e;\n"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Source([]byte(tt.in), Options{Indent: 2})
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("got\n%s\nwant\n%s", got, tt.want)
			}
			again, err := Source(got, Options{Indent: 2})
			if err != nil || !bytes.Equal(again, got) {
				t.Errorf("formatting again changed it:\n%s", again)
			}
		})
	}
}

func TestIndentOption(t *testing.T) {
	for _, tt := range []struct {
		indent int
		want   string
	}{
		{0, "fun F() {\n    x = 1;\n}\n"}, // the default
		{4, "fun F() {\n    x = 1;\n}\n"},
		{3, "fun F() {\n   x = 1;\n}\n"},
	} {
		got, err := Source([]byte("fun F() {\nx = 1;\n}\n"), Options{Indent: tt.indent})
		if err != nil || string(got) != tt.want {
			t.Errorf("indent %d: got %q, %v", tt.indent, got, err)
		}
	}
}

func TestSyntaxErrorsAreRefused(t *testing.T) {
	if _, err := Source([]byte("fun F() {\n  x = \n}\n"), Options{}); !errors.Is(err, ErrSyntax) {
		t.Errorf("got %v, want ErrSyntax", err)
	}
}

// TestCorpus formats every .p file under $PFMT_CORPUS, such as a checkout
// of github.com/p-org/P, and checks that each formats without error and
// that formatting again changes nothing.
func TestCorpus(t *testing.T) {
	root := os.Getenv("PFMT_CORPUS")
	if root == "" {
		t.Skip("set PFMT_CORPUS to a directory of P code")
	}
	files, refused := 0, 0
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || !strings.HasSuffix(path, ".p") || strings.HasPrefix(info.Name(), "._") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		files++
		out, err := Source(src, Options{})
		switch {
		case errors.Is(err, ErrSyntax):
			refused++
		case err != nil:
			t.Errorf("%s: %v", path, err)
		default:
			if again, err := Source(out, Options{}); err != nil || !bytes.Equal(again, out) {
				t.Errorf("%s: formatting again changed it", path)
			}
		}
		return nil
	})
	t.Logf("%d files, %d refused for syntax errors", files, refused)
}
