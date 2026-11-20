package main

import (
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
