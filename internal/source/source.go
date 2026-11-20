// Package source finds the P source behind the steps of a trace: the
// machines, states, handlers and statements they name.
package source

import (
	"encoding/xml"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	tree_sitter_p "github.com/bencornia/tree-sitter-p/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Location is a place in a source file. Lines and columns start at 1.
type Location struct {
	File         string
	Line, Column int
	// EndLine is the last line of the node located, such as a handler.
	EndLine int
}

// Contains reports whether line is within l.
func (l Location) Contains(file string, line int) bool {
	return l.File == file && l.Line <= line && line <= l.EndLine
}

// File is a parsed source file.
type File struct {
	Path string
	Text []byte
	Tree *sitter.Tree
}

// Node is a syntax node and the file it's in.
type Node struct {
	File *File
	*sitter.Node
}

// Location returns where the node is.
func (n Node) Location() Location {
	start, end := n.StartPosition(), n.EndPosition()
	return Location{File: n.File.Path, Line: int(start.Row) + 1, Column: int(start.Column) + 1, EndLine: int(end.Row) + 1}
}

func (n Node) text(m *sitter.Node) string {
	return m.Utf8Text(n.File.Text)
}

// Machine is a machine or spec monitor declaration.
type Machine struct {
	Name string
	Node
	// Start is the name of its start state.
	Start string
	// States and Functions are the machine's, by name.
	States    map[string]Node
	Functions map[string]Node
}

// StateNamed returns the machine's state called name. A nil machine has
// no states.
func (m *Machine) StateNamed(name string) (Node, bool) {
	if m == nil {
		return Node{}, false
	}
	state, ok := m.States[name]
	return state, ok
}

// FunctionsInOrder returns the machine's functions in source order.
func (m *Machine) FunctionsInOrder() []Node {
	fns := make([]Node, 0, len(m.Functions))
	for _, f := range m.Functions {
		fns = append(fns, f)
	}
	slices.SortFunc(fns, func(a, b Node) int { return int(a.StartByte()) - int(b.StartByte()) })
	return fns
}

// Index is the P source of a project.
type Index struct {
	Root     string
	Files    []*File
	Machines map[string]*Machine
	// Functions are the top-level functions, by name.
	Functions map[string]Node
	// events are the declared events' names.
	events map[string]bool
	// enums are the declared enums' constants, by name, with their values.
	enums map[string]int
}

// skipped are directories that hold generated code or checker output.
var skipped = map[string]bool{"PGenerated": true, "PCheckerOutput": true, "node_modules": true}

// Load parses the .p files under root, and under the directories of the
// projects its .pproj files include, as in
// <IncludeProject>../Common/Timer/Timer.pproj</IncludeProject>.
func Load(root string) (*Index, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	x := &Index{Root: root, Machines: map[string]*Machine{}, Functions: map[string]Node{}, events: map[string]bool{}, enums: map[string]int{}}
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(tree_sitter_p.Language())); err != nil {
		return nil, err
	}
	walked := []string{}
	for _, dir := range append([]string{root}, includedDirs(root)...) {
		if slices.ContainsFunc(walked, func(w string) bool { return within(dir, w) }) {
			continue
		}
		walked = append(walked, dir)
		if err := x.loadDir(parser, dir); err != nil {
			return nil, err
		}
	}
	return x, nil
}

// within reports whether dir is root or under it.
func within(dir, root string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// includedDirs returns the directories of the projects that root's .pproj
// files include, directly or through each other.
func includedDirs(root string) []string {
	var dirs []string
	seen := map[string]bool{}
	var visit func(pproj string)
	visit = func(pproj string) {
		if seen[pproj] {
			return
		}
		seen[pproj] = true
		data, err := os.ReadFile(pproj)
		if err != nil {
			return
		}
		var project struct {
			Includes []string `xml:"IncludeProject"`
		}
		if xml.Unmarshal(data, &project) != nil {
			return
		}
		for _, include := range project.Includes {
			path := filepath.Clean(filepath.Join(filepath.Dir(pproj), strings.TrimSpace(include)))
			dirs = append(dirs, filepath.Dir(path))
			visit(path)
		}
	}
	projects, _ := filepath.Glob(filepath.Join(root, "*.pproj"))
	for _, p := range projects {
		visit(p)
	}
	return dirs
}

// loadDir parses the .p files under dir.
func (x *Index) loadDir(parser *sitter.Parser, dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != dir && (strings.HasPrefix(d.Name(), ".") || skipped[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".p") || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		f := &File{Path: path, Text: text, Tree: parser.Parse(text, nil)}
		x.Files = append(x.Files, f)
		x.index(f)
		return nil
	})
}

// Close frees the syntax trees.
func (x *Index) Close() {
	for _, f := range x.Files {
		f.Tree.Close()
	}
}

func (x *Index) index(f *File) {
	walk(f.Tree.RootNode(), func(n *sitter.Node) bool {
		switch n.Kind() {
		case "impl_machine_declaration", "spec_machine_declaration", "scenario_machine_declaration":
			if name := n.ChildByFieldName("name"); name != nil {
				m := &Machine{Name: name.Utf8Text(f.Text), Node: Node{f, n}, States: map[string]Node{}, Functions: map[string]Node{}}
				x.indexMachine(m)
				x.Machines[m.Name] = m
			}
			return false
		case "p_function_declaration", "foreign_function_declaration":
			if name := n.ChildByFieldName("name"); name != nil {
				x.Functions[name.Utf8Text(f.Text)] = Node{f, n}
			}
			return false
		case "event_declaration":
			if name := n.ChildByFieldName("name"); name != nil {
				x.events[name.Utf8Text(f.Text)] = true
			}
			return false
		case "enum_type_declaration":
			x.indexEnum(f, n)
			return false
		}
		return true
	})
}

func (x *Index) indexMachine(m *Machine) {
	walk(m.Node.Node, func(n *sitter.Node) bool {
		switch n.Kind() {
		case "state_declaration":
			if name := n.ChildByFieldName("name"); name != nil {
				m.States[m.text(name)] = Node{m.File, n}
				if isStart(n) {
					m.Start = m.text(name)
				}
			}
			return false
		case "p_function_declaration", "foreign_function_declaration":
			if name := n.ChildByFieldName("name"); name != nil {
				m.Functions[m.text(name)] = Node{m.File, n}
			}
			return false
		}
		return true
	})
}

// isStart reports whether a state declaration is marked start.
func isStart(state *sitter.Node) bool {
	for i := uint(0); i < state.ChildCount(); i++ {
		if state.Child(i).Kind() == "start" {
			return true
		}
	}
	return false
}

// FunctionsInOrder returns the top-level functions in source order, file by
// file.
func (x *Index) FunctionsInOrder() []Node {
	fns := make([]Node, 0, len(x.Functions))
	for _, f := range x.Functions {
		fns = append(fns, f)
	}
	slices.SortFunc(fns, func(a, b Node) int {
		if a.File != b.File {
			return strings.Compare(a.File.Path, b.File.Path)
		}
		return int(a.StartByte()) - int(b.StartByte())
	})
	return fns
}

// Function returns the function a machine's handler names: one of the
// machine's, or else a top-level one.
func (x *Index) Function(m *Machine, name string) (Node, bool) {
	if f, ok := m.Functions[name]; ok {
		return f, true
	}
	f, ok := x.Functions[name]
	return f, ok
}

// StateHandler returns a state's entry or exit handler: its anonymous body,
// or the function it names. It returns false if the state has none.
func (x *Index) StateHandler(m *Machine, state Node, entry bool) (Node, bool) {
	anon, named := "state_exit_anon", "state_exit_fun"
	if entry {
		anon, named = "state_entry_anon", "state_entry_fun"
	}
	for _, item := range children(state.Node) {
		switch item.Kind() {
		case anon:
			return Node{state.File, item}, true
		case named:
			if fn := item.ChildByFieldName("function_name"); fn != nil {
				if f, ok := x.Function(m, state.text(fn)); ok {
					return f, true
				}
			}
			return Node{state.File, item}, true
		}
	}
	return Node{}, false
}

// EventHandler returns what handles event in a state: an `on` item, or the
// function it names, or a defer or ignore. It returns false if nothing in
// the state mentions the event.
func (x *Index) EventHandler(m *Machine, state Node, event string) (Node, bool) {
	for _, item := range children(state.Node) {
		if !mentionsEvent(state, item, event) {
			continue
		}
		if fn := item.ChildByFieldName("function_name"); fn != nil {
			if f, ok := x.Function(m, state.text(fn)); ok {
				return f, true
			}
		}
		return Node{state.File, item}, true
	}
	return Node{}, false
}

// mentionsEvent reports whether a state item, or a receive case, lists
// event.
func mentionsEvent(in Node, item *sitter.Node, event string) bool {
	found := false
	walk(item, func(n *sitter.Node) bool {
		switch n.Kind() {
		case "event_list", "non_default_event_list":
			walk(n, func(e *sitter.Node) bool {
				if (e.Kind() == "identifier" || e.Kind() == "null_literal") && in.text(e) == event {
					found = true
				}
				return !found
			})
			return false
		case "anon_event_handler", "no_param_anon_event_handler", "function_body":
			// A handler's body can't list the item's events.
			return false
		}
		return !found
	})
	return found
}

// AnyEvent is the name Statement takes for a send whose event is a
// variable, as in a helper that sends whatever event it's given.
const AnyEvent = "\x00any event"

// Statements returns every statement in scope that Statement would find,
// in source order.
func (x *Index) Statements(scope Node, kind, name string) []Node {
	var found []Node
	walk(scope.Node, func(n *sitter.Node) bool {
		if n.Kind() == kind && x.matches(scope, n, name) {
			found = append(found, Node{scope.File, n})
			return false
		}
		return true
	})
	return found
}

// Statement finds a statement of the given kind in scope that matches: a
// send, raise or announce of an event, a goto to a state, a print whose
// format string could produce text, or a receive (or a receive case for an
// event). It returns the first in source order.
func (x *Index) Statement(scope Node, kind, name string) (Node, bool) {
	var found *sitter.Node
	walk(scope.Node, func(n *sitter.Node) bool {
		if found != nil {
			return false
		}
		if n.Kind() == kind && x.matches(scope, n, name) {
			found = n
			return false
		}
		return true
	})
	if found == nil {
		return Node{}, false
	}
	return Node{scope.File, found}, true
}

func (x *Index) matches(scope Node, n *sitter.Node, name string) bool {
	switch n.Kind() {
	case "send_statement":
		// send machine, event, payload...
		for i := uint(0); i+1 < n.ChildCount(); i++ {
			if n.Child(i).Kind() == "," {
				event := scope.text(n.Child(i + 1))
				if name == AnyEvent {
					return !x.events[event]
				}
				return event == name
			}
		}
	case "raise_statement", "announce_statement":
		return n.ChildCount() > 1 && scope.text(n.Child(1)) == name
	case "goto_statement":
		if s := firstChild(n, "state_name"); s != nil {
			return scope.text(s) == name
		}
	case "print_statement":
		return printMatches(scope, n, name)
	case "receive_statement":
		return name == "" || mentionsCase(scope, n, name)
	case "recv_case":
		return mentionsEvent(scope, n, name)
	}
	return false
}

func mentionsCase(scope Node, receive *sitter.Node, event string) bool {
	for _, c := range children(receive) {
		if c.Kind() == "recv_case" && mentionsEvent(scope, c, event) {
			return true
		}
	}
	return false
}

var formatArgument = regexp.MustCompile(`\\\{\d+\\\}`)

// printMatches reports whether a print statement could have printed text:
// its string, with each {0}, {1}... matching anything.
func printMatches(scope Node, print *sitter.Node, text string) bool {
	var literal *sitter.Node
	walk(print, func(n *sitter.Node) bool {
		if literal == nil && n.Kind() == "string_literal" {
			literal = n
		}
		return literal == nil
	})
	if literal == nil {
		return false
	}
	format := strings.Trim(scope.text(literal), `"`)
	pattern := "^" + formatArgument.ReplaceAllString(regexp.QuoteMeta(format), ".*") + "$"
	re, err := regexp.Compile(pattern)
	return err == nil && re.MatchString(text)
}

func walk(n *sitter.Node, visit func(*sitter.Node) bool) {
	if !visit(n) {
		return
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		walk(n.Child(i), visit)
	}
}

func children(n *sitter.Node) []*sitter.Node {
	var c []*sitter.Node
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c = append(c, n.NamedChild(i))
	}
	return c
}

func firstChild(n *sitter.Node, kind string) *sitter.Node {
	for _, c := range children(n) {
		if c.Kind() == kind {
			return c
		}
	}
	return nil
}
