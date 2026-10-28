package analysis

import (
	"fmt"
	"slices"

	"github.com/bencornia/p-lsp/internal/style"
	protocol "github.com/tliron/glsp/protocol_3_16"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// The codes of the checks' diagnostics, which say which setting in a
// .plint.toml's [checks] turns each off.
const (
	UnusedCode           = "unused"
	NeverSentCode        = "never-sent"
	UnreachableStateCode = "unreachable-state"
	UnreachableCodeCode  = "unreachable-code"
	ShadowingCode        = "shadowing"
)

// Checks returns what the checks turned on in checks find in doc: unused
// declarations, events nothing sends, states that can't be reached, code
// that can't run, and machine variables hidden by local names. Whether a
// global name is used is decided across workspace, the documents that can
// use what doc declares.
//
// Uses are counted by name, so a name used anywhere counts as used, even
// if that use means something else; the checks miss some unused names, but
// don't report used ones.
func Checks(workspace []*Document, doc *Document, checks style.Checks) []protocol.Diagnostic {
	if !slices.Contains(workspace, doc) {
		workspace = append(slices.Clip(workspace), doc)
	}
	c := &checker{workspace: workspace, doc: doc, checks: checks, diagnostics: []protocol.Diagnostic{}}
	c.declarations(doc.Tree.RootNode())
	if checks.UnreachableCode != style.LevelOff {
		c.unreachableCode()
	}
	slices.SortStableFunc(c.diagnostics, func(a, b protocol.Diagnostic) int {
		if a.Range.Start.Line != b.Range.Start.Line {
			return int(a.Range.Start.Line) - int(b.Range.Start.Line)
		}
		return int(a.Range.Start.Character) - int(b.Range.Start.Character)
	})
	return c.diagnostics
}

type checker struct {
	workspace   []*Document
	doc         *Document
	checks      style.Checks
	diagnostics []protocol.Diagnostic
}

// report adds a diagnostic at level, unless the level is off. Unnecessary
// code is shown faded, in editors that support it.
func (c *checker) report(level style.Level, code string, rng protocol.Range, unnecessary bool, format string, args ...any) {
	var severity protocol.DiagnosticSeverity
	switch level {
	case style.LevelWarning:
		severity = protocol.DiagnosticSeverityWarning
	case style.LevelError:
		severity = protocol.DiagnosticSeverityError
	default:
		return
	}
	source := diagnosticSource
	diagnostic := protocol.Diagnostic{
		Range:    rng,
		Severity: &severity,
		Code:     &protocol.IntegerOrString{Value: code},
		Source:   &source,
		Message:  fmt.Sprintf(format, args...),
	}
	if unnecessary {
		diagnostic.Tags = []protocol.DiagnosticTag{protocol.DiagnosticTagUnnecessary}
	}
	c.diagnostics = append(c.diagnostics, diagnostic)
}

func (c *checker) unused(name *sitter.Node, what string) {
	c.report(c.checks.Unused, UnusedCode, c.doc.Range(name), true, "%s `%s` is never used", what, c.text(name))
}

func (c *checker) text(n *sitter.Node) string {
	return n.Utf8Text(c.doc.Text)
}

// declarations checks the top-level declarations under node.
func (c *checker) declarations(node *sitter.Node) {
	for _, decl := range namedChildren(node) {
		name := decl.ChildByFieldName("name")
		switch decl.Kind() {
		case "type_def_declaration", "function_declaration":
			c.declarations(decl)
		case "p_type_def", "foreign_type_def":
			if name != nil && c.usesOf(c.text(name)).all <= 1 {
				c.unused(name, "type")
			}
		case "enum_type_declaration":
			c.enum(decl)
		case "event_declaration":
			c.event(name)
		case "event_set_declaration":
			if name != nil && c.usesOf(c.text(name)).all <= 1 {
				c.unused(name, "event set")
			}
		case "p_function_declaration", "foreign_function_declaration", "pure_declaration":
			if name != nil && c.usesOf(c.text(name)).all <= 1 {
				c.unused(name, "function")
			}
			c.scopes(decl, nil)
		case "global_param_declaration":
			for _, n := range identifierListNames(decl) {
				if c.usesOf(c.text(n)).all <= 1 {
					c.unused(n, "parameter")
				}
			}
		case "impl_machine_declaration", "spec_machine_declaration", "scenario_machine_declaration":
			c.machine(decl)
		}
	}
}

// enum reports an enum type that's never used, by its own name or its
// constants'.
func (c *checker) enum(decl *sitter.Node) {
	name := decl.ChildByFieldName("name")
	if name == nil {
		return
	}
	names := []*sitter.Node{name}
	for _, list := range namedChildren(decl) {
		for _, elem := range namedChildren(list) {
			names = appendName(names, elem, "name")
		}
	}
	for _, n := range names {
		if c.usesOf(c.text(n)).all > 1 {
			return
		}
	}
	c.unused(name, "type")
}

// event reports an event that's never used, or never sent, raised or
// announced: only handled, deferred, ignored or observed, which is never.
func (c *checker) event(name *sitter.Node) {
	if name == nil {
		return
	}
	uses := c.usesOf(c.text(name))
	switch {
	case uses.all <= 1:
		c.unused(name, "event")
	case uses.all-uses.listed <= 1:
		c.report(c.checks.NeverSent, NeverSentCode, c.doc.Range(name), false,
			"event `%s` is never sent, raised or announced", c.text(name))
	}
}

// machine checks a machine's variables, functions and states.
func (c *checker) machine(decl *sitter.Node) {
	body := machineBody(decl)
	if body == nil {
		return
	}
	variables := machineMembers(body, nsValue)
	machineVariables := map[string]bool{}
	for _, n := range variables {
		if owner := c.doc.parent(c.doc.parent(n)); owner == nil || owner.Kind() != "var_declaration" {
			continue
		}
		machineVariables[c.text(n)] = true
		// The P verifier reads machines' variables as fields, from outside
		// the machine.
		if countIn(c.doc, decl, c.text(n)) <= 1 && c.usesOf(c.text(n)).fields == 0 {
			c.unused(n, "variable")
		}
	}
	for _, entry := range namedChildren(body) {
		if entry.NamedChildCount() == 0 || entry.NamedChild(0).Kind() != "function_declaration" {
			continue
		}
		fn := entry.NamedChild(0)
		if fn.NamedChildCount() == 0 {
			continue
		}
		if name := fn.NamedChild(0).ChildByFieldName("name"); name != nil && countIn(c.doc, decl, c.text(name)) <= 1 {
			c.unused(name, "function")
		}
	}
	c.scopes(decl, machineVariables)
	c.unreachableStates(decl, body)
	if decl.Kind() != "impl_machine_declaration" {
		c.observed(decl)
	}
}

// scopes checks the local variables and parameters of the functions and
// event handlers under node: locals that are never used, and names that
// hide one of machineVariables.
func (c *checker) scopes(node *sitter.Node, machineVariables map[string]bool) {
	walkNodes(node, func(n *sitter.Node) bool {
		if n.Kind() == "ERROR" {
			return false
		}
		switch n.Kind() {
		case "p_function_declaration", "foreign_function_declaration", "pure_declaration",
			"anon_event_handler", "no_param_anon_event_handler":
		default:
			return true
		}
		var locals []*sitter.Node
		if body := firstChildOfKind(n, "function_body"); body != nil {
			locals = localVariables(body)
		}
		for _, local := range locals {
			if countIn(c.doc, n, c.text(local)) <= 1 {
				c.unused(local, "variable")
			}
		}
		for _, name := range scopeDeclarations(n, nsValue) {
			if machineVariables[c.text(name)] {
				c.report(c.checks.Shadowing, ShadowingCode, c.doc.Range(name), false,
					"`%s` hides the machine variable `%s`", c.text(name), c.text(name))
			}
		}
		return true
	})
}

// unreachableStates reports the states of a machine that can't be reached
// from its start state. A goto in one of the machine's functions could run
// from any state, so its target counts as reachable.
func (c *checker) unreachableStates(machine, body *sitter.Node) {
	states := map[string]*sitter.Node{}
	var order []string
	var start string
	var queue []string
	for _, entry := range namedChildren(body) {
		if entry.NamedChildCount() == 0 {
			continue
		}
		decl := entry.NamedChild(0)
		if decl.Kind() != "state_declaration" {
			queue = append(queue, c.stateTargets(decl)...)
			continue
		}
		name := decl.ChildByFieldName("name")
		if name == nil || name.IsMissing() {
			continue
		}
		states[c.text(name)] = decl
		order = append(order, c.text(name))
		if firstTokenOfKind(decl, "start") != nil && start == "" {
			start = c.text(name)
		}
	}
	if start == "" {
		// The compiler reports a machine with no start state.
		return
	}
	reached := map[string]bool{}
	for queue = append(queue, start); len(queue) > 0; queue = queue[1:] {
		name := queue[0]
		if reached[name] || states[name] == nil {
			continue
		}
		reached[name] = true
		queue = append(queue, c.stateTargets(states[name])...)
	}
	for _, name := range order {
		if !reached[name] {
			c.report(c.checks.UnreachableStates, UnreachableStateCode, c.doc.Range(states[name].ChildByFieldName("name")), true,
				"state `%s` can't be reached from the start state `%s`", name, start)
		}
	}
}

// stateTargets returns the names of the states that gotos under node go to.
func (c *checker) stateTargets(node *sitter.Node) []string {
	var targets []string
	walkNodes(node, func(n *sitter.Node) bool {
		if n.Kind() == "state_name" {
			if state := n.ChildByFieldName("state"); state != nil {
				targets = append(targets, c.text(state))
			}
			return false
		}
		return true
	})
	return targets
}

// observed reports the events a spec observes that are never sent, raised
// or announced, which it would wait for forever.
func (c *checker) observed(spec *sitter.Node) {
	observes := firstChildOfKind(spec, "event_set_literal")
	if observes == nil {
		return
	}
	specName := spec.ChildByFieldName("name")
	for _, event := range namedChildren(observes) {
		ident := firstChildOfKind(event, "identifier")
		if ident == nil {
			continue
		}
		decls := resolve(c.workspace, c.doc, ident)
		if len(decls) != 1 || decls[0].parent().Kind() != "event_declaration" {
			continue
		}
		if uses := c.usesOf(c.text(ident)); uses.all-uses.listed <= 1 && specName != nil {
			c.report(c.checks.NeverSent, NeverSentCode, c.doc.Range(ident), false,
				"`%s` observes `%s`, which is never sent, raised or announced", c.text(specName), c.text(ident))
		}
	}
}

// unreachableCode reports the statements after one that always leaves the
// block: return, raise, goto, break, continue, or an if whose branches
// both do.
func (c *checker) unreachableCode() {
	unreachable := map[uintptr]bool{}
	c.doc.walk(func(n *sitter.Node) bool {
		if n.Kind() == "ERROR" || unreachable[n.Id()] {
			return false
		}
		if n.Kind() != "function_body" && n.Kind() != "compound_statement" {
			return true
		}
		var leaving *sitter.Node
		var dead []*sitter.Node
		for _, stmt := range namedChildren(n) {
			switch stmt.Kind() {
			case "comment", "var_declaration":
				continue
			}
			if leaving != nil {
				dead = append(dead, stmt)
				unreachable[stmt.Id()] = true
			} else if leaves(stmt) {
				leaving = stmt
			}
		}
		if len(dead) == 0 {
			return true
		}
		rng := protocol.Range{Start: c.doc.Range(dead[0]).Start, End: c.doc.Range(dead[len(dead)-1]).End}
		if leaving.Kind() == "if_statement" || leaving.Kind() == "compound_statement" {
			c.report(c.checks.UnreachableCode, UnreachableCodeCode, rng, true, "unreachable code: every path through the statement before it leaves")
		} else {
			c.report(c.checks.UnreachableCode, UnreachableCodeCode, rng, true, "unreachable code after `%s`", leaving.Child(0).Kind())
		}
		return true
	})
}

// leaves reports whether control never goes past stmt to the statement
// after it.
func leaves(stmt *sitter.Node) bool {
	switch stmt.Kind() {
	case "return_statement", "raise_statement", "goto_statement", "break_statement", "continue_statement":
		return true
	case "compound_statement":
		return slices.ContainsFunc(namedChildren(stmt), leaves)
	case "if_statement":
		then, otherwise := stmt.ChildByFieldName("then_branch"), stmt.ChildByFieldName("else_branch")
		return then != nil && otherwise != nil && leaves(then) && leaves(otherwise)
	}
	return false
}

// uses is how many identifiers spell a name: all of them, those in lists of
// events that are handled or observed rather than sent, and those that
// name a field.
type uses struct {
	all, listed, fields int
}

// usesOf adds up the uses of name across the workspace.
func (c *checker) usesOf(name string) uses {
	var total uses
	for _, d := range c.workspace {
		u := d.nameUses()[name]
		total.all += u.all
		total.listed += u.listed
		total.fields += u.fields
	}
	return total
}

// nameUses counts the identifiers in d spelling each name. Callers mustn't
// modify the result, which is cached.
func (d *Document) nameUses() map[string]uses {
	d.uses.once.Do(func() {
		counts := map[string]uses{}
		var visit func(n, parent *sitter.Node)
		visit = func(n, parent *sitter.Node) {
			if n.Kind() == "identifier" {
				u := counts[n.Utf8Text(d.Text)]
				u.all++
				switch parent.Kind() {
				case "event_id", "non_default_event":
					u.listed++
				case "named_tuple_access_expression", "named_tuple_lvalue":
					if !sameNode(parent.NamedChild(0), n) {
						u.fields++
					}
				}
				counts[n.Utf8Text(d.Text)] = u
				return
			}
			for i := uint(0); i < n.ChildCount(); i++ {
				visit(n.Child(i), n)
			}
		}
		visit(d.Tree.RootNode(), nil)
		d.uses.counts = counts
	})
	return d.uses.counts
}

// countIn returns how many identifiers under node spell name.
func countIn(doc *Document, node *sitter.Node, name string) int {
	count := 0
	forEachIdentifier(node, func(n *sitter.Node) {
		if n.Utf8Text(doc.Text) == name {
			count++
		}
	})
	return count
}
