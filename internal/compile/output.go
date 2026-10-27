// Package compile runs the P compiler to type-check projects and turns its
// error messages into diagnostics.
package compile

import (
	"bufio"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Error is one error the compiler reported.
type Error struct {
	// File is the absolute path of the file the error is in, or "" if the
	// compiler gave no location.
	File string
	// Line and Column are 0-based. Column counts UTF-16 code units, as the
	// compiler's C# strings do, which is what LSP expects.
	Line, Column int
	Message      string
}

var (
	// [Error:] [PSrc/Client.p:12:5] message
	// Paths are relative to the compiler's working directory; lines and
	// columns are 1-based.
	locatedError = regexp.MustCompile(`^\[Error:\] \[(.+):(-?\d+):(-?\d+)\] (.*)$`)
	// [Parser Error:] [Client.p] parse error: line 12:4 message
	// Only the file's base name is given; the column is 0-based.
	parseError = regexp.MustCompile(`^\[Parser Error:\] \[([^\]]+)\] parse error: line (\d+):(\d+) (.*)$`)
	// [NotSupportedError:] /abs/path/Client.p (line 12:4): message
	notSupported = regexp.MustCompile(`^\[NotSupportedError:\] (.+) \(line (\d+):(\d+)\): (.*)$`)
	// Any other error, such as "[Error:] <no source>:-1:-1 message".
	otherError = regexp.MustCompile(`^\[(?:[A-Za-z]+ )?(?:Error|NotSupportedError|NotImplementedError):\] (.*)$`)
	// Error: message, for errors in the project file, such as one that
	// lists no .p files.
	plainError = regexp.MustCompile(`^Error: (.+)$`)
	// Before P 3.1, the "[Error:]" part was on a line of its own, and the
	// rest of the error on the next line.
	errorHeading = regexp.MustCompile(`^\[(?:[A-Za-z]+ )?(?:Error|NotSupportedError|NotImplementedError):\]$`)
)

// ParseOutput extracts the errors from the compiler's output. dir is the
// directory the compiler ran in, and files are the project's files, used to
// find the file a parse error names by base name.
func ParseOutput(output, dir string, files []string) []Error {
	var errors []Error
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	heading := ""
	for scanner.Scan() {
		line := strings.TrimSpace(stripANSI(scanner.Text()))
		switch {
		case line == "":
			continue
		case errorHeading.MatchString(line):
			heading = line
			continue
		case heading != "":
			line = heading + " " + line
			heading = ""
		}
		if e, ok := parseLine(line, dir, files); ok {
			errors = append(errors, e)
		}
	}
	if heading != "" {
		// A heading with nothing after it.
		errors = append(errors, Error{Message: strings.Trim(heading, "[]:")})
	}
	return errors
}

func parseLine(line, dir string, files []string) (Error, bool) {
	if m := locatedError.FindStringSubmatch(line); m != nil {
		lineNo, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		if m[1] == "<no source>" || lineNo < 1 {
			return Error{Message: m[4]}, true
		}
		path := m[1]
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		return Error{File: filepath.Clean(path), Line: lineNo - 1, Column: max(col-1, 0), Message: m[4]}, true
	}
	if m := parseError.FindStringSubmatch(line); m != nil {
		lineNo, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		return Error{File: byBaseName(m[1], files), Line: lineNo - 1, Column: col, Message: "parse error: " + m[4]}, true
	}
	if m := notSupported.FindStringSubmatch(line); m != nil {
		lineNo, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		return Error{File: filepath.Clean(m[1]), Line: lineNo - 1, Column: col, Message: m[4]}, true
	}
	if m := otherError.FindStringSubmatch(line); m != nil {
		return Error{Message: m[1]}, true
	}
	if m := plainError.FindStringSubmatch(line); m != nil {
		return Error{Message: m[1]}, true
	}
	return Error{}, false
}

// byBaseName returns the file in files named name, or "" if there isn't
// exactly one.
func byBaseName(name string, files []string) string {
	found := ""
	for _, f := range files {
		if filepath.Base(f) == name {
			if found != "" {
				return ""
			}
			found = f
		}
	}
	return found
}

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

// stripANSI removes terminal color codes, in case the compiler colors its
// output even when it isn't writing to a terminal.
func stripANSI(s string) string {
	return ansi.ReplaceAllString(s, "")
}
