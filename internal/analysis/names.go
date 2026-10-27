package analysis

import (
	"fmt"
	"slices"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// maxSuggestions is how many names a misspelled name is offered as fixes.
const maxSuggestions = 3

// undefinedNameFixes finds names that refer to nothing: variables,
// functions, types, machines, states, modules, and the fields of named
// tuples. Each comes with fixes that change it to the closest names that
// would fit. Events are left to undefinedEventFixes, which can also
// declare them.
func undefinedNameFixes(workspace []*Document, doc *Document) []Fix {
	fixes := []Fix{}
	walkNodes(doc.Tree.RootNode(), func(n *sitter.Node) bool {
		if n.IsError() {
			// Already reported as a syntax error.
			return false
		}
		if n.Kind() != "identifier" || eventReference(n) != nil {
			return true
		}
		name := n.Utf8Text(doc.Text)
		var message string
		var candidates []declaration
		if isFieldName(n) {
			fields, typeName, ok := knownFields(workspace, doc, n)
			if !ok || len(named(fields, name)) > 0 {
				return true
			}
			message = fmt.Sprintf("no field `%s` in `%s`", name, typeName)
			candidates = fields
		} else {
			// A declared name resolves to itself.
			if len(resolve(workspace, doc, n)) > 0 {
				return true
			}
			kind := "name"
			switch namespaceOf(n) {
			case nsState:
				kind = "state"
			case nsModule:
				kind = "module"
			}
			message = fmt.Sprintf("undefined %s `%s`", kind, name)
			candidates = fitting(n, namesInScope(workspace, doc, n))
		}
		fixes = append(fixes, renameFixes(doc, n, nameDiagnostic(doc, n, message), candidates)...)
		return true
	})
	return fixes
}

// Declaration kinds that fit where a type, a function, or a machine to
// create goes.
var (
	declaredTypeKinds = []string{"p_type_def", "foreign_type_def", "enum_type_declaration", "interface_declaration",
		"impl_machine_declaration", "spec_machine_declaration", "scenario_machine_declaration"}
	functionKinds = []string{"p_function_declaration", "foreign_function_declaration", "pure_declaration"}
	machineKinds  = []string{"interface_declaration", "impl_machine_declaration"}
)

// fitting returns the candidates that could go where the name n is: only
// types in a type, functions in a call, and machines after `new`.
func fitting(n *sitter.Node, candidates []declaration) []declaration {
	var kinds []string
	parent := n.Parent()
	switch parent.Kind() {
	case "named_type":
		kinds = declaredTypeKinds
	case "function_call_expression", "function_call_statement":
		kinds = functionKinds
	case "ctor_expression", "ctor_statement":
		kinds = machineKinds
	default:
		return candidates
	}
	fit := []declaration{}
	for _, c := range candidates {
		if slices.Contains(kinds, c.node.Parent().Kind()) {
			fit = append(fit, c)
		}
	}
	return fit
}

// knownFields returns the fields that the field name ident can be, with
// the name of the type they belong to, when that type is known to be a
// named tuple or a spec machine. For anything else, such as a machine
// whose variables only the verifier can read, it returns false.
func knownFields(workspace []*Document, doc *Document, ident *sitter.Node) ([]declaration, string, bool) {
	parent := ident.Parent()
	var t typeRef
	switch parent.Kind() {
	case "named_tuple_access_expression", "named_tuple_lvalue":
		base := parent.NamedChild(0)
		if spec, ok := specReference(workspace, doc, base); ok {
			return machineVariables(spec.doc, spec.node.Parent()), spec.name, true
		}
		var ok bool
		if t, ok = typeOf(workspace, doc, base); !ok {
			return nil, "", false
		}
	case "named_tuple_body":
		var ok bool
		if t, ok = expectedType(workspace, doc, parent.Parent()); !ok {
			return nil, "", false
		}
	default:
		return nil, "", false
	}
	u, ok := underlying(workspace, t)
	if !ok || u.node.Kind() != "named_tuple_type" {
		return nil, "", false
	}
	return tupleFields(u), t.node.Utf8Text(t.doc.Text), true
}

func nameDiagnostic(doc *Document, n *sitter.Node, message string) protocol.Diagnostic {
	severity := protocol.DiagnosticSeverityError
	source := diagnosticSource
	return protocol.Diagnostic{Range: doc.Range(n), Severity: &severity, Source: &source, Message: message}
}

// renameFixes offers to change the name n to each of the closest
// candidates. With no close candidate, the problem is still reported, with
// no fix.
func renameFixes(doc *Document, n *sitter.Node, diagnostic protocol.Diagnostic, candidates []declaration) []Fix {
	suggestions := closestNames(n.Utf8Text(doc.Text), candidates)
	if len(suggestions) == 0 {
		return []Fix{{Diagnostic: diagnostic}}
	}
	fixes := make([]Fix, 0, len(suggestions))
	for _, s := range suggestions {
		fixes = append(fixes, Fix{
			Diagnostic: diagnostic,
			Title:      fmt.Sprintf("Change to `%s`", s),
			Edits:      []protocol.TextEdit{{Range: doc.Range(n), NewText: s}},
		})
	}
	return fixes
}

// closestNames returns up to maxSuggestions of the candidates' names that
// name is plausibly a misspelling of, closest first. Differences in case
// count for less than other edits.
func closestNames(name string, candidates []declaration) []string {
	type scored struct {
		name     string
		distance int
	}
	limit := 1
	switch {
	case len(name) > 8:
		limit = 3
	case len(name) > 4:
		limit = 2
	}
	seen := map[string]bool{}
	var close []scored
	for _, c := range candidates {
		if seen[c.name] || c.name == name {
			continue
		}
		seen[c.name] = true
		folded := editDistance(strings.ToLower(name), strings.ToLower(c.name))
		if folded > limit {
			continue
		}
		// Twice the distance ignoring case, plus one if case differs too.
		d := 2 * folded
		if editDistance(name, c.name) > folded {
			d++
		}
		close = append(close, scored{c.name, d})
	}
	slices.SortStableFunc(close, func(a, b scored) int {
		if a.distance != b.distance {
			return a.distance - b.distance
		}
		return strings.Compare(a.name, b.name)
	})
	names := []string{}
	for i := 0; i < len(close) && i < maxSuggestions; i++ {
		names = append(names, close[i].name)
	}
	return names
}

// editDistance returns the number of single-character insertions,
// deletions, substitutions, and swaps of adjacent characters it takes to
// turn a into b.
func editDistance(a, b string) int {
	s, t := []rune(a), []rune(b)
	// d[i][j] is the distance between s[:i] and t[:j].
	d := make([][]int, len(s)+1)
	for i := range d {
		d[i] = make([]int, len(t)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(s); i++ {
		for j := 1; j <= len(t); j++ {
			cost := 1
			if s[i-1] == t[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && s[i-1] == t[j-2] && s[i-2] == t[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(s)][len(t)]
}
