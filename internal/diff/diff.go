// Package diff produces unified diffs of text, line by line.
package diff

import (
	"bytes"
	"fmt"
	"strings"
)

// context is how many unchanged lines surround each change in a hunk.
const context = 3

// Unified returns a unified diff turning a, named oldName, into b, named
// newName, or nil if they're the same.
func Unified(oldName, newName string, a, b []byte) []byte {
	if bytes.Equal(a, b) {
		return nil
	}
	x, y := splitLines(a), splitLines(b)
	ops := edits(x, y)

	var out bytes.Buffer
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", oldName, newName)
	for start := 0; start < len(ops); {
		// Find the next change, and the run of changes, with at most
		// 2*context unchanged lines between them, that make one hunk.
		first := start
		for first < len(ops) && ops[first].kind == ' ' {
			first++
		}
		if first == len(ops) {
			break
		}
		last := first
		for i := first; i < len(ops); i++ {
			if ops[i].kind != ' ' {
				last = i
			} else if i-last > 2*context {
				break
			}
		}
		from, to := max(first-context, 0), min(last+context+1, len(ops))
		writeHunk(&out, ops[from:to])
		start = to
	}
	return out.Bytes()
}

type op struct {
	kind rune // ' ', '-' or '+'
	line string
	// x and y are the line's 0-based numbers in a and b, as far as it's in
	// them.
	x, y int
}

func writeHunk(out *bytes.Buffer, ops []op) {
	oldStart, newStart, oldCount, newCount := -1, -1, 0, 0
	for _, o := range ops {
		if o.kind != '+' {
			oldCount++
			if oldStart < 0 {
				oldStart = o.x
			}
		}
		if o.kind != '-' {
			newCount++
			if newStart < 0 {
				newStart = o.y
			}
		}
	}
	// A side with no lines in the hunk is numbered by the line before it.
	if oldStart < 0 {
		oldStart = ops[0].x - 1
	}
	if newStart < 0 {
		newStart = ops[0].y - 1
	}
	fmt.Fprintf(out, "@@ -%s +%s @@\n", hunkRange(oldStart, oldCount), hunkRange(newStart, newCount))
	for _, o := range ops {
		out.WriteRune(o.kind)
		out.WriteString(o.line)
		if !strings.HasSuffix(o.line, "\n") {
			out.WriteString("\n\\ No newline at end of file\n")
		}
	}
}

func hunkRange(start, count int) string {
	if count == 1 {
		return fmt.Sprint(start + 1)
	}
	return fmt.Sprintf("%d,%d", start+1, count)
}

// splitLines splits text into lines, each with its "\n" except perhaps the
// last.
func splitLines(text []byte) []string {
	var lines []string
	for len(text) > 0 {
		i := bytes.IndexByte(text, '\n')
		if i < 0 {
			lines = append(lines, string(text))
			break
		}
		lines = append(lines, string(text[:i+1]))
		text = text[i+1:]
	}
	return lines
}

// edits returns a shortest edit script turning x into y, using Myers'
// algorithm: unchanged lines, deletions and insertions, in order.
func edits(x, y []string) []op {
	n, m := len(x), len(y)
	max := n + m
	// v[k+max] is the furthest x reached on diagonal k. trace keeps, for
	// each number of edits d, the diagonals -d to d of v before that step,
	// which is all backtrack needs; copying all of v each time would take
	// memory in the square of the file's length when every line changes.
	v := make([]int, 2*max+2)
	var trace [][]int
	for d := 0; d <= max; d++ {
		trace = append(trace, append([]int(nil), v[max-d:max+d+1]...))
		for k := -d; k <= d; k += 2 {
			var i int
			if k == -d || k != d && v[k-1+max] < v[k+1+max] {
				i = v[k+1+max] // down: an insertion
			} else {
				i = v[k-1+max] + 1 // right: a deletion
			}
			j := i - k
			for i < n && j < m && x[i] == y[j] {
				i, j = i+1, j+1
			}
			v[k+max] = i
			if i >= n && j >= m {
				return backtrack(x, y, trace, d)
			}
		}
	}
	return nil
}

// backtrack walks the trace from the end back to the start to recover the
// edit script.
func backtrack(x, y []string, trace [][]int, d int) []op {
	var ops []op
	i, j := len(x), len(y)
	for ; d > 0; d-- {
		// trace[d] holds diagonals -d to d.
		at := func(k int) int { return trace[d][k+d] }
		k := i - j
		var prevK int
		if k == -d || k != d && at(k-1) < at(k+1) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevI := at(prevK)
		prevJ := prevI - prevK
		for i > prevI && j > prevJ {
			i, j = i-1, j-1
			ops = append(ops, op{' ', x[i], i, j})
		}
		if i == prevI {
			j--
			ops = append(ops, op{'+', y[j], i, j})
		} else {
			i--
			ops = append(ops, op{'-', x[i], i, j})
		}
	}
	for i > 0 && j > 0 {
		i, j = i-1, j-1
		ops = append(ops, op{' ', x[i], i, j})
	}
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}
	return ops
}
