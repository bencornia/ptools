package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	const trace = "../../testdata/pingpong/bug.trace.json"
	var stdout, stderr strings.Builder
	if status := run([]string{"list", trace}, nil, &stdout, &stderr); status != 0 || !strings.Contains(stdout.String(), "PSpec/Spec.p:2") {
		t.Errorf("prdb list = %d, stdout %q, stderr %q", status, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if status := run([]string{"diagram", "--steps", "1-5", "--monitors=false", trace}, nil, &stdout, &stderr); status != 0 || !strings.HasPrefix(stdout.String(), "<svg") {
		t.Errorf("prdb diagram = %d, stdout %.40q, stderr %q", status, stdout.String(), stderr.String())
	}

	for _, c := range []struct {
		args []string
		want int
	}{
		{[]string{"list"}, 2},                            // no trace or --testcase
		{[]string{"list", "--testcase", "tc", trace}, 2}, // both
		{[]string{"diagram", "--steps", "x", trace}, 2},
		{[]string{"-list", trace}, 2}, // a long flag takes two dashes
		{[]string{"list", "missing.trace.json"}, 1},
	} {
		stderr.Reset()
		if status := run(c.args, nil, &stdout, &stderr); status != c.want || stderr.Len() == 0 {
			t.Errorf("prdb %v = %d, stderr %q; want %d and an error", c.args, status, stderr.String(), c.want)
		}
	}
}

func TestCheck(t *testing.T) {
	if _, err := exec.LookPath("p"); err != nil {
		t.Skip("the P compiler isn't installed")
	}
	project := t.TempDir()
	if err := os.CopyFS(project, os.DirFS("../../testdata/params")); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	check := func(args ...string) int {
		stdout.Reset()
		stderr.Reset()
		return run(append([]string{"check", "--project", project, "--schedules", "10"}, args...), nil, &stdout, &stderr)
	}

	if status := check("--json", "tcQuiet"); status != 0 || stdout.String() != "{\"bug\":null}\n" ||
		!strings.Contains(stderr.String(), "PChecker found no bug in tcQuiet in 10 schedules") {
		t.Errorf("check tcQuiet = %d, stdout %q, stderr %.200q", status, stdout.String(), stderr.String())
	}
	trace := filepath.Join(project, "PCheckerOutput", "prdb", "tcHello___given_2.trace.json")
	if status := check("tcHello"); status != 1 || stdout.String() != trace+"\n" {
		t.Errorf("check tcHello = %d, stdout %q; want 1, %s", status, stdout.String(), trace)
	}
	want, _ := json.Marshal(map[string]any{"bug": map[string]string{"test": "tcHello___given_2", "trace": trace}})
	if status := check("--json", "tcHello"); status != 1 || stdout.String() != string(want)+"\n" {
		t.Errorf("check --json tcHello = %d, stdout %q; want 1, %s", status, stdout.String(), want)
	}
	if status := check("tcNope"); status != 1 || !strings.Contains(stderr.String(), "no test case tcNope") {
		t.Errorf("check tcNope = %d, stderr %.300q", status, stderr.String())
	}
	if status := check("--compiler", "p-no-such-tool", "tcQuiet"); status != 1 || !strings.Contains(stderr.String(), "pass --compiler") {
		t.Errorf("check without the P tool = %d, stderr %q", status, stderr.String())
	}
	if status := check(); status != 2 {
		t.Errorf("check with no test case = %d, want 2", status)
	}
}
