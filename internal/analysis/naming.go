package analysis

import (
	"fmt"

	"github.com/bencornia/ptools/internal/style"
	protocol "github.com/tliron/glsp/protocol_3_16"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// NamingCode is the code of a naming diagnostic, which tells them apart from
// errors.
const NamingCode = "naming"

// NamingProblem is a declaration whose name isn't written in the case the
// style asks for, and the name it would be in that case, if there is one.
type NamingProblem struct {
	Diagnostic protocol.Diagnostic
	Name       string
	Suggestion string
}

// namingKind is a kind of declaration the style gives a case to.
type namingKind struct {
	// what the declaration is called in a message.
	what string
	// rule picks the kind's case out of the style.
	rule func(style.Naming) style.Case
}

var (
	typeNames     = namingKind{"type", func(n style.Naming) style.Case { return n.Types }}
	enumConstants = namingKind{"enum constant", func(n style.Naming) style.Case { return n.EnumConstants }}
	eventNames    = namingKind{"event", func(n style.Naming) style.Case { return n.Events }}
	machineNames  = namingKind{"machine", func(n style.Naming) style.Case { return n.Machines }}
	stateNames    = namingKind{"state", func(n style.Naming) style.Case { return n.States }}
	moduleNames   = namingKind{"module", func(n style.Naming) style.Case { return n.Modules }}
	functionNames = namingKind{"function", func(n style.Naming) style.Case { return n.Functions }}
	variableNames = namingKind{"variable", func(n style.Naming) style.Case { return n.Variables }}
	testNames     = namingKind{"test case", func(n style.Naming) style.Case { return n.TestCases }}
)

// namedDeclarations maps a declaration's node kind and the field holding its
// name to the kind of name it is.
var namedDeclarations = map[[2]string]namingKind{
	{"p_type_def", "name"}:                       typeNames,
	{"foreign_type_def", "name"}:                 typeNames,
	{"enum_type_declaration", "name"}:            typeNames,
	{"enum_elem", "name"}:                        enumConstants,
	{"numbered_enum_elem", "name"}:               enumConstants,
	{"event_declaration", "name"}:                eventNames,
	{"event_set_declaration", "name"}:            eventNames,
	{"interface_declaration", "name"}:            machineNames,
	{"impl_machine_declaration", "name"}:         machineNames,
	{"spec_machine_declaration", "name"}:         machineNames,
	{"scenario_machine_declaration", "name"}:     machineNames,
	{"state_declaration", "name"}:                stateNames,
	{"named_module_declaration", "name"}:         moduleNames,
	{"p_function_declaration", "name"}:           functionNames,
	{"foreign_function_declaration", "name"}:     functionNames,
	{"pure_declaration", "name"}:                 functionNames,
	{"function_param", "name"}:                   variableNames,
	{"identifier_type", "name"}:                  variableNames,
	{"identifier_list", "names"}:                 variableNames,
	{"safety_test_declaration", "test_name"}:     testNames,
	{"refinement_test_declaration", "test_name"}: testNames,
	{"implementation_declaration", "impl_name"}:  testNames,
}

// Naming returns the declarations in doc whose names aren't in the case
// naming asks for, as warnings.
func Naming(doc *Document, naming style.Naming) []NamingProblem {
	problems := []NamingProblem{}
	doc.walk(func(n *sitter.Node) bool {
		if n.Kind() == "ERROR" {
			return false
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			field := n.FieldNameForChild(uint32(i))
			kind, ok := namedDeclarations[[2]string{n.Kind(), field}]
			if !ok || (n.Kind() == "identifier_list" && !declaresVariables(doc.parent(n))) {
				continue
			}
			name := n.Child(i)
			if name.Kind() != "identifier" || name.IsMissing() {
				continue
			}
			if p, ok := namingProblem(doc, name, kind, naming); ok {
				problems = append(problems, p)
			}
		}
		return true
	})
	return problems
}

// declaresVariables reports whether decl declares the variables in its
// identifier_list; elsewhere, as in a test's `assert Spec, ...`, the list
// names things declared elsewhere.
func declaresVariables(decl *sitter.Node) bool {
	return decl != nil && (decl.Kind() == "var_declaration" || decl.Kind() == "global_param_declaration")
}

// NamingDiagnostics returns the diagnostics of Naming's problems.
func NamingDiagnostics(doc *Document, naming style.Naming) []protocol.Diagnostic {
	diagnostics := []protocol.Diagnostic{}
	for _, p := range Naming(doc, naming) {
		diagnostics = append(diagnostics, p.Diagnostic)
	}
	return diagnostics
}

func namingProblem(doc *Document, ident *sitter.Node, kind namingKind, naming style.Naming) (NamingProblem, bool) {
	name := ident.Utf8Text(doc.Text)
	want := kind.rule(naming)
	if want.Conforms(name, naming.Prefixes) {
		return NamingProblem{}, false
	}
	message := fmt.Sprintf("%s name `%s` should be %s", kind.what, name, want)
	suggestion := want.Suggest(name, naming.Prefixes)
	if suggestion != "" {
		message += fmt.Sprintf(": `%s`", suggestion)
	}
	severity := protocol.DiagnosticSeverityWarning
	source := diagnosticSource
	code := protocol.IntegerOrString{Value: NamingCode}
	return NamingProblem{
		Diagnostic: protocol.Diagnostic{
			Range:    doc.Range(ident),
			Severity: &severity,
			Code:     &code,
			Source:   &source,
			Message:  message,
		},
		Name:       name,
		Suggestion: suggestion,
	}, true
}
