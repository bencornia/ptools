package compile

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseOutput(t *testing.T) {
	dir := filepath.FromSlash("/proj/App")
	files := []string{
		filepath.FromSlash("/proj/App/PSrc/Client.p"),
		filepath.FromSlash("/proj/App/PSrc/Server.p"),
		filepath.FromSlash("/proj/Common/Timer/PSrc/Timer.p"),
		filepath.FromSlash("/proj/App/PTst/Timer.p"),
	}
	// Shaped like the P compiler's output: info lines, then errors, some
	// separated by blank lines, one in color.
	output := "----------------------------------------\n" +
		"==== Loading project file: /proj/App/App.pproj\n" +
		"Parsing ...\n" +
		"Type checking ...\n" +
		"[Error:] [PSrc/Client.p:12:5] could not find event 'eFoo'\n" +
		"\n" +
		"[Error:] [../Common/Timer/PSrc/Timer.p:3:1] type mismatch\r\n" +
		"\x1b[31m[Error:] [PSrc/Server.p:7:10] function 'F' not found\x1b[0m\n" +
		"[Parser Error:] [Server.p] parse error: line 4:2 mismatched input 'x'\n" +
		"[Parser Error:] [Timer.p] parse error: line 1:0 two files have this name\n" +
		"[NotSupportedError:] /proj/App/PSrc/Client.p (line 9:3): \"forall\" keyword is only supported by PVerifier backend.\n" +
		"[Error:] [<no source>:-1:-1] internal error\n" +
		"[NotImplementedError:] something unsupported\n" +
		"Error: At least one .p file must be provided as input files, no input files found after parsing the project file\n" +
		"Code generation for PChecker...\n"

	got := ParseOutput(output, dir, files)
	want := []Error{
		{File: files[0], Line: 11, Column: 4, Message: "could not find event 'eFoo'"},
		{File: files[2], Line: 2, Column: 0, Message: "type mismatch"},
		{File: files[1], Line: 6, Column: 9, Message: "function 'F' not found"},
		{File: files[1], Line: 3, Column: 2, Message: "parse error: mismatched input 'x'"},
		{File: "", Line: 0, Column: 0, Message: "parse error: two files have this name"},
		{File: files[0], Line: 8, Column: 3, Message: "\"forall\" keyword is only supported by PVerifier backend."},
		{Message: "internal error"},
		{Message: "something unsupported"},
		{Message: "At least one .p file must be provided as input files, no input files found after parsing the project file"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseOutput:\n got %+v\nwant %+v", got, want)
	}
}

func TestParseOutputIgnoresInfo(t *testing.T) {
	output := "Parsing ...\nType checking ...\nCode generation for PChecker...\nGenerated App.cs.\n"
	if got := ParseOutput(output, "/proj", nil); len(got) != 0 {
		t.Errorf("got %+v, want no errors", got)
	}
}

// P before 3.1 (here, 3.0.4) puts the "[Error:]" on a line of its own.
func TestParseOutputTwoLineErrors(t *testing.T) {
	dir := filepath.FromSlash("/proj/App")
	files := []string{filepath.FromSlash("/proj/App/PSrc/A.p")}
	output := "----------------------------------------\n" +
		"....... includes p file: /proj/App/PSrc/A.p\n" +
		"----------------------------------------\n" +
		"Parsing ...\n" +
		"Type checking ...\n" +
		"[Error:]\n" +
		"[PSrc/A.p:4:13] got type: bool, expected: int\n" +
		"[Parser Error:]\r\n" +
		"[A.p] parse error: line 3:16 missing ';' at '}'\r\n" +
		"\x1b[31m[Error:]\x1b[0m\n" +
		"\n" +
		"[../Common/C.p:1:27] got type: bool, expected: int\n" +
		"[Error:]\n"

	got := ParseOutput(output, dir, files)
	want := []Error{
		{File: files[0], Line: 3, Column: 12, Message: "got type: bool, expected: int"},
		{File: files[0], Line: 2, Column: 16, Message: "parse error: missing ';' at '}'"},
		{File: filepath.FromSlash("/proj/Common/C.p"), Line: 0, Column: 26, Message: "got type: bool, expected: int"},
		{Message: "Error"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}
