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
