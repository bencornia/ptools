package analysis

import (
	"slices"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Doc is a declaration as documentation shows it.
type Doc struct {
	Name string
	// Source is the declaration as P source, without the body of a
	// machine, state or function.
	Source string
	// Comment is its doc comment, without the comment markers.
	Comment string
	// URI is the document declaring it, and Line its line there, from 1.
	URI  string
	Line int
}

// Package is what a project's files declare, for documentation, each kind
// in the order declared.
type Package struct {
	Types      []Doc
	Events     []Event
	EventSets  []Doc
	Interfaces []Doc
	// Machines are the machines, specs and scenarios.
	Machines  []Machine
	Functions []Doc
	Params    []Doc
	Modules   []Doc
	Tests     []Doc
}

// Event is a declared event and the places that use it.
type Event struct {
	Doc
	// Payload is the declared payload type, or "" for none.
	Payload string
	Uses    []EventUse
}

// EventUse is a place an event is sent, raised, announced, handled,
// deferred, ignored or observed.
type EventUse struct {
	// Verb is send, raise, announce, on (a state's handler), receive (a
	// receive statement's case), defer, ignore or observe (a spec's
	// observes list).
	Verb string
	// Machine is the machine, spec or scenario it's in, if any, and
	// MachineKind its keyword: machine, spec or scenario.
	Machine, MachineKind string
	// State and Function are the state and function it's in, if any.
	State, Function string
}

// Machine is a machine, spec or scenario.
type Machine struct {
	Doc
	// Kind is machine, spec or scenario.
	Kind string
	// Observes are the events a spec or scenario observes.
	Observes  []string
	States    []State
	Variables []Doc
	Functions []Doc
}

// State is one of a machine's states.
type State struct {
	Doc
	Start bool
	// Temperature is hot, cold or "".
	Temperature string
	// Items are its entry and exit handlers, event handlers, defers and
	// ignores, in order.
	Items []StateItem
}

// StateItem is an entry in a state's body.
type StateItem struct {
	// Kind is entry, exit, on, defer or ignore.
	Kind   string
	Events []string
	// Goto is the state an on goes to, if it does.
	Goto string
	// Function is the function it runs, if it names one rather than
	// running a block of its own.
	Function string
}

// Docs returns what files declare, and where workspace, the documents
// compiled with them, uses their events.
func Docs(files, workspace []*Document) Package {
	var p Package
	for _, doc := range files {
		p.add(doc, doc.Tree.RootNode())
	}
	uses := eventUses(workspace)
	for i := range p.Events {
		p.Events[i].Uses = uses[p.Events[i].Name]
	}
	return p
}

func (p *Package) add(doc *Document, node *sitter.Node) {
	for _, decl := range namedChildren(node) {
		switch decl.Kind() {
		case "type_def_declaration", "function_declaration", "test_declaration":
			p.add(doc, decl)
		case "p_type_def", "foreign_type_def", "enum_type_declaration":
			p.Types = appendDoc(p.Types, doc, decl.ChildByFieldName("name"))
		case "event_declaration":
			if d, ok := docOf(doc, decl.ChildByFieldName("name")); ok {
				e := Event{Doc: d}
				if colon, semicolon := firstTokenOfKind(decl, ":"), firstTokenOfKind(decl, ";"); colon != nil && semicolon != nil {
					e.Payload = strings.TrimSpace(string(doc.Text[colon.EndByte():semicolon.StartByte()]))
				}
				p.Events = append(p.Events, e)
			}
		case "event_set_declaration":
			p.EventSets = appendDoc(p.EventSets, doc, decl.ChildByFieldName("name"))
		case "interface_declaration":
			p.Interfaces = appendDoc(p.Interfaces, doc, decl.ChildByFieldName("name"))
		case "impl_machine_declaration", "spec_machine_declaration", "scenario_machine_declaration":
			if m, ok := machineDoc(doc, decl); ok {
				p.Machines = append(p.Machines, m)
			}
		case "p_function_declaration", "foreign_function_declaration", "pure_declaration":
			p.Functions = appendDoc(p.Functions, doc, decl.ChildByFieldName("name"))
		case "global_param_declaration":
			for _, name := range identifierListNames(decl) {
				p.Params = appendDoc(p.Params, doc, name)
			}
		case "named_module_declaration":
			p.Modules = appendDoc(p.Modules, doc, decl.ChildByFieldName("name"))
		case "safety_test_declaration", "refinement_test_declaration":
			p.Tests = appendDoc(p.Tests, doc, decl.ChildByFieldName("test_name"))
		}
	}
}

// docOf documents the declaration name names.
func docOf(doc *Document, name *sitter.Node) (Doc, bool) {
	if name == nil || name.IsMissing() {
		return Doc{}, false
	}
	d := declaration{doc: doc, node: name, name: name.Utf8Text(doc.Text)}
	return Doc{
		Name:    d.name,
		Source:  source(d),
		Comment: docComment(d),
		URI:     doc.URI,
		Line:    int(name.StartPosition().Row) + 1,
	}, true
}

func appendDoc(docs []Doc, doc *Document, name *sitter.Node) []Doc {
	if d, ok := docOf(doc, name); ok {
		return append(docs, d)
	}
	return docs
}

func machineDoc(doc *Document, decl *sitter.Node) (Machine, bool) {
	d, ok := docOf(doc, decl.ChildByFieldName("name"))
	if !ok {
		return Machine{}, false
	}
	m := Machine{Doc: d, Kind: decl.Child(0).Kind()}
	if decl.Kind() == "impl_machine_declaration" {
		m.Kind = "machine"
	} else if observes := firstChildOfKind(decl, "event_set_literal"); observes != nil {
		m.Observes = listedEvents(doc, observes)
	}
	body := machineBody(decl)
	if body == nil {
		return m, true
	}
	for _, entry := range namedChildren(body) {
		if entry.NamedChildCount() == 0 {
			continue
		}
		member := entry.NamedChild(0)
		switch member.Kind() {
		case "var_declaration":
			for _, name := range identifierListNames(member) {
				m.Variables = appendDoc(m.Variables, doc, name)
			}
		case "function_declaration":
			if member.NamedChildCount() > 0 {
				m.Functions = appendDoc(m.Functions, doc, member.NamedChild(0).ChildByFieldName("name"))
			}
		case "state_declaration":
			if s, ok := stateDoc(doc, member); ok {
				m.States = append(m.States, s)
			}
		}
	}
	return m, true
}

func stateDoc(doc *Document, decl *sitter.Node) (State, bool) {
	d, ok := docOf(doc, decl.ChildByFieldName("name"))
	if !ok {
		return State{}, false
	}
	s := State{Doc: d, Start: firstTokenOfKind(decl, "start") != nil}
	if t := decl.ChildByFieldName("temperature"); t != nil {
		s.Temperature = t.Utf8Text(doc.Text)
	}
	text := func(n *sitter.Node) string {
		if n == nil {
			return ""
		}
		return n.Utf8Text(doc.Text)
	}
	for _, item := range namedChildren(decl) {
		var it StateItem
		switch item.Kind() {
		case "state_entry_anon", "state_entry_fun":
			it = StateItem{Kind: "entry", Function: text(item.ChildByFieldName("function_name"))}
		case "state_exit_anon", "state_exit_fun":
			it = StateItem{Kind: "exit", Function: text(item.ChildByFieldName("function_name"))}
		case "state_defer", "state_ignore":
			it = StateItem{Kind: strings.TrimPrefix(item.Kind(), "state_"), Events: listedEvents(doc, item)}
		case "on_event_do_action_with_fun", "on_event_do_action_with_anon", "on_event_goto_state",
			"on_event_goto_state_with_anon", "on_event_goto_state_with_fun":
			it = StateItem{Kind: "on", Events: listedEvents(doc, item), Function: text(item.ChildByFieldName("function_name"))}
			if target := firstChildOfKind(item, "state_name"); target != nil {
				it.Goto = text(target.ChildByFieldName("state"))
			}
		default:
			continue
		}
		s.Items = append(s.Items, it)
	}
	return s, true
}

// listedEvents returns the events listed under node, in an event list,
// event set literal, defer or ignore.
func listedEvents(doc *Document, node *sitter.Node) []string {
	var names []string
	walkNodes(node, func(n *sitter.Node) bool {
		switch n.Kind() {
		case "event_id", "non_default_event":
			names = append(names, n.Utf8Text(doc.Text))
			return false
		case "anon_event_handler", "no_param_anon_event_handler":
			return false
		}
		return true
	})
	return names
}

// eventUses returns the places workspace uses each event, by name, in the
// order they come, each once.
func eventUses(workspace []*Document) map[string][]EventUse {
	uses := map[string][]EventUse{}
	for _, doc := range workspace {
		doc.walk(func(n *sitter.Node) bool {
			if n.Kind() == "ERROR" {
				return false
			}
			if eventReference(doc, n) == nil {
				return true
			}
			use, ok := eventUse(doc, n)
			if name := n.Utf8Text(doc.Text); ok && !slices.Contains(uses[name], use) {
				uses[name] = append(uses[name], use)
			}
			return true
		})
	}
	return uses
}

// eventUse describes the use of the event that ref, an event reference,
// names, and reports false for uses that only list it, as in an event set.
func eventUse(doc *Document, ref *sitter.Node) (EventUse, bool) {
	var use EventUse
	parent := doc.parent(ref)
	switch parent.Kind() {
	case "send_statement":
		use.Verb = "send"
	case "raise_statement":
		use.Verb = "raise"
	case "announce_statement":
		use.Verb = "announce"
	case "event_id":
		list := doc.parent(parent)
		switch owner := doc.parent(list); {
		case owner.Kind() == "recv_case":
			use.Verb = "receive"
		case strings.HasPrefix(owner.Kind(), "on_event_"):
			use.Verb = "on"
		default:
			return use, false
		}
	case "non_default_event":
		list := doc.parent(parent)
		switch owner := doc.parent(list).Kind(); {
		case owner == "state_defer":
			use.Verb = "defer"
		case owner == "state_ignore":
			use.Verb = "ignore"
		case list.Kind() == "event_set_literal" && (owner == "spec_machine_declaration" || owner == "scenario_machine_declaration"):
			use.Verb = "observe"
		default:
			return use, false
		}
	default:
		return use, false
	}
	for n := doc.parent(parent); n != nil; n = doc.parent(n) {
		name := func() string {
			if name := n.ChildByFieldName("name"); name != nil {
				return name.Utf8Text(doc.Text)
			}
			return ""
		}
		switch n.Kind() {
		case "state_declaration":
			use.State = name()
		case "p_function_declaration":
			if use.Function == "" {
				use.Function = name()
			}
		case "impl_machine_declaration":
			use.Machine, use.MachineKind = name(), "machine"
		case "spec_machine_declaration", "scenario_machine_declaration":
			use.Machine, use.MachineKind = name(), n.Child(0).Kind()
		}
	}
	return use, true
}
