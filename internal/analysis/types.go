package analysis

import (
	"strconv"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// This file infers just enough about types to resolve tuple field names, as
// in `req.amount`: the declared types of names, followed through type
// aliases, field access, indexing, calls and casts. It isn't a type checker,
// and gives up (returning ok == false) on anything else.

// typeRef is a type as written in the source: a type node and its document.
type typeRef struct {
	doc  *Document
	node *sitter.Node
}

var typeKinds = map[string]bool{
	"named_type":       true,
	"named_tuple_type": true,
	"tuple_type":       true,
	"map_type":         true,
	"sequence_type":    true,
	"set_type":         true,
}

// maxAliasDepth bounds how many `type A = B;` aliases are followed, so a
// cycle of aliases can't loop forever.
const maxAliasDepth = 32

// resolveField resolves the field name in `base.field`, where access is a
// named_tuple_access_expression or named_tuple_lvalue.
func resolveField(workspace []*Document, doc *Document, access, field *sitter.Node) []declaration {
	name := field.Utf8Text(doc.Text)
	if spec, ok := specReference(workspace, doc, access.NamedChild(0)); ok {
		if v, ok := machineVariable(spec.doc, spec.node.Parent(), name); ok {
			return []declaration{v}
		}
		return nil
	}

	base, ok := typeOf(workspace, doc, access.NamedChild(0))
	if !ok {
		return nil
	}
	if f, ok := fieldOf(workspace, base, name); ok {
		return []declaration{f}
	}
	return nil
}

// specReference reports whether expr names a spec machine, which the P
// verifier lets you read variables from, as in `Consistency.kv`.
func specReference(workspace []*Document, doc *Document, expr *sitter.Node) (declaration, bool) {
	if expr == nil || expr.Kind() != "identifier" {
		return declaration{}, false
	}
	if decls := resolve(workspace, doc, expr); len(decls) == 1 && decls[0].node.Parent().Kind() == "spec_machine_declaration" {
		return decls[0], true
	}
	return declaration{}, false
}

// typeOf returns the type of an expression or lvalue.
func typeOf(workspace []*Document, doc *Document, expr *sitter.Node) (typeRef, bool) {
	if expr == nil {
		return typeRef{}, false
	}
	switch expr.Kind() {
	case "identifier":
		if decls := resolve(workspace, doc, expr); len(decls) == 1 {
			return declaredType(decls[0])
		}
	case "lvalue", "var_lvalue", "rvalue", "paren_expression":
		return typeOf(workspace, doc, expr.NamedChild(0))
	case "named_tuple_access_expression", "named_tuple_lvalue":
		field := expr.ChildByFieldName("field")
		if field == nil {
			field = expr.NamedChild(1) // named_tuple_lvalue has no field name
		}
		if field != nil {
			if decls := resolveField(workspace, doc, expr, field); len(decls) == 1 {
				return declaredType(decls[0])
			}
		}
	case "tuple_access_expression", "tuple_lvalue":
		return tupleElement(workspace, doc, expr)
	case "seq_access_expression", "map_or_sequence_lvalue":
		container := expr.ChildByFieldName("seq")
		if container == nil {
			container = expr.NamedChild(0)
		}
		if t, ok := typeOf(workspace, doc, container); ok {
			return elementType(workspace, t)
		}
	case "cast_expression":
		return typeRef{doc, expr.NamedChild(expr.NamedChildCount() - 1)}, true
	case "function_call_expression":
		if fun := expr.ChildByFieldName("fun"); fun != nil {
			if decls := resolve(workspace, doc, fun); len(decls) == 1 {
				return returnType(decls[0])
			}
		}
	}
	return typeRef{}, false
}

// tupleElement returns the type of `t.0`, `t.1`, ... for an unnamed tuple.
func tupleElement(workspace []*Document, doc *Document, access *sitter.Node) (typeRef, bool) {
	index := access.ChildByFieldName("field")
	if index == nil {
		index = access.NamedChild(1) // tuple_lvalue has no field name
	}
	base, ok := typeOf(workspace, doc, access.NamedChild(0))
	if index == nil || !ok {
		return typeRef{}, false
	}
	tuple, ok := underlying(workspace, base)
	if !ok || tuple.node.Kind() != "tuple_type" {
		return typeRef{}, false
	}
	i, err := strconv.Atoi(index.Utf8Text(doc.Text))
	elements := tuple.node.ChildrenByFieldName("tup_types", tuple.node.Walk())
	if err != nil || i < 0 || i >= len(elements) {
		return typeRef{}, false
	}
	return typeRef{tuple.doc, &elements[i]}, true
}

// elementType returns the type of `c[i]`: a sequence's element type or a
// map's value type.
func elementType(workspace []*Document, container typeRef) (typeRef, bool) {
	u, ok := underlying(workspace, container)
	if !ok {
		return typeRef{}, false
	}
	switch u.node.Kind() {
	case "sequence_type":
		return typeChild(u.doc, u.node)
	case "map_type":
		if value := u.node.ChildByFieldName("value_type"); value != nil {
			return typeRef{u.doc, value}, true
		}
	}
	return typeRef{}, false
}

// fieldOf returns the declaration of the field called name in values of
// type t.
func fieldOf(workspace []*Document, t typeRef, name string) (declaration, bool) {
	u, ok := underlying(workspace, t)
	if !ok {
		return declaration{}, false
	}
	switch u.node.Kind() {
	case "named_tuple_type":
		return tupleField(u, name)
	case "named_type":
		if typeName := u.node.ChildByFieldName("name"); typeName != nil {
			return verifierField(workspace, typeName.Utf8Text(u.doc.Text), name)
		}
	}
	return declaration{}, false
}

func tupleField(tuple typeRef, name string) (declaration, bool) {
	list := firstChildOfKind(tuple.node, "identifier_type_list")
	if list == nil {
		return declaration{}, false
	}
	for _, field := range namedChildren(list) {
		if n := field.ChildByFieldName("name"); n != nil && n.Utf8Text(tuple.doc.Text) == name {
			return declaration{doc: tuple.doc, node: n, name: name}, true
		}
	}
	return declaration{}, false
}

// verifierField resolves a field of a value whose type is the machine,
// interface, event or event set called typeName. The P verifier lets
// `n.voted` read machine n's variable voted, and `e.src` read field src of
// event e's payload.
func verifierField(workspace []*Document, typeName, name string) (declaration, bool) {
	globals := globalDeclarationsIn(workspace, nsValue)
	for _, d := range named(globals, typeName) {
		switch decl := d.node.Parent(); decl.Kind() {
		case "impl_machine_declaration", "spec_machine_declaration", "scenario_machine_declaration":
			if v, ok := machineVariable(d.doc, decl, name); ok {
				return v, true
			}
		case "interface_declaration":
			// The compiler uses the machine with the interface's name.
			for _, m := range named(globals, typeName) {
				if machineBody(m.node.Parent()) != nil {
					if v, ok := machineVariable(m.doc, m.node.Parent(), name); ok {
						return v, true
					}
				}
			}
		case "event_declaration":
			if payload, ok := typeChild(d.doc, decl); ok {
				return fieldOf(workspace, payload, name)
			}
		case "event_set_declaration":
			// The first event in the set whose payload has the field.
			events := firstChildOfKind(decl, "event_set_literal")
			if events == nil {
				continue
			}
			for _, event := range events.ChildrenByFieldName("events", events.Walk()) {
				// Skipping the set's own name guards against a set that
				// lists itself, which the compiler would reject anyway.
				if eventName := event.Utf8Text(d.doc.Text); eventName != typeName {
					if f, ok := verifierField(workspace, eventName, name); ok {
						return f, true
					}
				}
			}
		}
	}
	return declaration{}, false
}

func machineVariable(doc *Document, machine *sitter.Node, name string) (declaration, bool) {
	for _, d := range named(declarationsOf(doc, machineMembers(machineBody(machine), nsValue)), name) {
		if d.node.Parent().Kind() == "identifier_list" {
			return d, true
		}
	}
	return declaration{}, false
}

// underlying follows type aliases to the type they stand for. A named type
// that isn't an alias, such as a machine or event, is returned as is.
func underlying(workspace []*Document, t typeRef) (typeRef, bool) {
	for range maxAliasDepth {
		if t.node.Kind() != "named_type" {
			return t, true
		}
		name := t.node.ChildByFieldName("name")
		if name == nil {
			return typeRef{}, false
		}
		var aliases []declaration
		for _, d := range named(globalDeclarationsIn(workspace, nsValue), name.Utf8Text(t.doc.Text)) {
			if d.node.Parent().Kind() == "p_type_def" {
				aliases = append(aliases, d)
			}
		}
		switch len(aliases) {
		case 0:
			return t, true
		case 1:
			var ok bool
			if t, ok = typeChild(aliases[0].doc, aliases[0].node.Parent()); !ok {
				return typeRef{}, false
			}
		default:
			return typeRef{}, false
		}
	}
	return typeRef{}, false
}

// declaredType returns the type a variable, parameter or field is declared
// with: `var x: T`, `(x: T)` or `(name: T)` in a named tuple type.
func declaredType(d declaration) (typeRef, bool) {
	decl := d.node.Parent()
	if decl.Kind() == "identifier_list" {
		decl = decl.Parent()
	}
	switch decl.Kind() {
	case "var_declaration", "function_param", "identifier_type":
		return typeChild(d.doc, decl)
	}
	return typeRef{}, false
}

// returnType returns the return type of the function a declaration names.
func returnType(d declaration) (typeRef, bool) {
	fn := d.node.Parent()
	switch fn.Kind() {
	case "p_function_declaration", "pure_declaration":
		return typeChild(d.doc, fn)
	case "foreign_function_declaration":
		if t, ok := typeChild(d.doc, fn); ok {
			return t, true
		}
		// `fun F(...) return (result: T);`
		if result := firstChildOfKind(fn, "function_param"); result != nil {
			return typeChild(d.doc, result)
		}
	}
	return typeRef{}, false
}

// typeChild returns the first child of node that is a type. Primitive types
// such as int have no node of their own, so they never match.
func typeChild(doc *Document, node *sitter.Node) (typeRef, bool) {
	for _, child := range namedChildren(node) {
		if typeKinds[child.Kind()] {
			return typeRef{doc, child}, true
		}
	}
	return typeRef{}, false
}
