package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	const messy = "machine M{\nstart state S{}\n}\n"
	const tidy = "machine M {\n  start state S {}\n}\n"
	var stdout, stderr strings.Builder
	if status := run([]string{"--indent", "2"}, strings.NewReader(messy), &stdout, &stderr); status != 0 || stdout.String() != tidy {
		t.Errorf("pfmt --indent 2 = %d, %q, stderr %q", status, stdout.String(), stderr.String())
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "M.p")
	if err := os.WriteFile(file, []byte(messy), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if status := run([]string{"-l", dir}, nil, &stdout, &stderr); status != 0 || stdout.String() != file+"\n" {
		t.Errorf("pfmt -l = %d, %q", status, stdout.String())
	}
	if status := run([]string{"--write", "--indent=2", file}, nil, &stdout, &stderr); status != 0 {
		t.Errorf("pfmt --write = %d, stderr %q", status, stderr.String())
	}
	if got, _ := os.ReadFile(file); string(got) != tidy {
		t.Errorf("pfmt --write wrote %q", got)
	}

	for _, c := range []struct {
		args  []string
		stdin string
		want  int
	}{
		{[]string{"-w"}, "", 2},                       // -w needs files
		{[]string{"-indent", "2"}, messy, 2},          // a long flag takes two dashes
		{nil, "machine M {", 2},                       // syntax error on stdin
		{[]string{filepath.Join(dir, "no.p")}, "", 1}, // a file that isn't there
	} {
		stderr.Reset()
		if status := run(c.args, strings.NewReader(c.stdin), &stdout, &stderr); status != c.want || stderr.Len() == 0 {
			t.Errorf("pfmt %v = %d, stderr %q; want %d and an error", c.args, status, stderr.String(), c.want)
		}
	}
}
