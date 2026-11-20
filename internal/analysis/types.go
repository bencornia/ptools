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
		if v, ok := machineVariable(spec.doc, spec.parent(), name); ok {
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
	for expr != nil && (expr.Kind() == "lvalue" || expr.Kind() == "var_lvalue") {
		expr = expr.NamedChild(0)
	}
	if expr == nil || expr.Kind() != "identifier" {
		return declaration{}, false
	}
	if decls := resolve(workspace, doc, expr); len(decls) == 1 && decls[0].parent().Kind() == "spec_machine_declaration" {
		return decls[0], true
	}
	return declaration{}, false
}

// resolveValueField resolves a field name in a tuple value, as in
// `(source = this, amount = 1)`, through the type its context expects.
func resolveValueField(workspace []*Document, doc *Document, body, field *sitter.Node) []declaration {
	t, ok := expectedType(workspace, doc, doc.parent(body))
	if !ok {
		return nil
	}
	if f, ok := fieldOf(workspace, t, field.Utf8Text(doc.Text)); ok {
		return []declaration{f}
	}
	return nil
}

// expectedType returns the type that value's context expects it to have:
// the target of an assignment, a function's return or parameter type, an
// event's payload, a state's or machine's entry parameter, or the type of the
// enclosing tuple's field.
func expectedType(workspace []*Document, doc *Document, value *sitter.Node) (typeRef, bool) {
	node := value
	if p := doc.parent(node); p != nil && p.Kind() == "rvalue" {
		node = p
	}
	context := doc.parent(node)
	if context == nil {
		return typeRef{}, false
	}

	switch context.Kind() {
	case "assign_statement":
		return typeOf(workspace, doc, context.NamedChild(0))
	case "return_statement":
		for fn := doc.parent(context); fn != nil; fn = doc.parent(fn) {
			switch fn.Kind() {
			case "p_function_declaration":
				if name := fn.ChildByFieldName("name"); name != nil {
					return returnType(declaration{doc: doc, node: name})
				}
				return typeRef{}, false
			case "anon_event_handler", "no_param_anon_event_handler":
				// Event handlers don't return values.
				return typeRef{}, false
			}
		}
	case "rvalue_list":
		return argumentType(workspace, doc, context, node)
	case "named_tuple_body":
		// The value of field f: the identifier just before it names f.
		outer, ok := expectedType(workspace, doc, doc.parent(context))
		if !ok {
			return typeRef{}, false
		}
		var prev *sitter.Node
		for _, child := range namedChildren(context) {
			if sameNode(child, node) && prev != nil && prev.Kind() == "identifier" {
				if f, ok := fieldOf(workspace, outer, prev.Utf8Text(doc.Text)); ok {
					return declaredType(f)
				}
				break
			}
			prev = child
		}
	case "unnamed_tuple_body":
		outer, ok := expectedType(workspace, doc, doc.parent(context))
		if !ok {
			return typeRef{}, false
		}
		return tupleTypeElement(workspace, outer, indexOf(context, node))
	case "paren_expression":
		return expectedType(workspace, doc, context)
	case "cast_expression":
		// `(source = this, amount = 1) as tReq`: the value is read as the
		// type it's cast to.
		if sameNode(context.NamedChild(0), node) {
			return typeRef{doc, context.NamedChild(context.NamedChildCount() - 1)}, true
		}
	case "insert_statement":
		// `c += (key, value)`: a map's key and value, or a sequence's index
		// and element.
		c, ok := typeOf(workspace, doc, context.NamedChild(0))
		if !ok {
			return typeRef{}, false
		}
		if indexOf(context, node) == 1 {
			return collectionType(workspace, c, "map_type", "key_type")
		}
		return elementType(workspace, c)
	case "add_statement":
		// `s += (element)` adds to a set.
		if c, ok := typeOf(workspace, doc, context.NamedChild(0)); ok {
			return collectionType(workspace, c, "set_type", "")
		}
	case "remove_statement":
		// `c -= key` removes a set's element or a map's key. (A sequence's
		// is removed by index.)
		if c, ok := typeOf(workspace, doc, context.NamedChild(0)); ok {
			if t, ok := collectionType(workspace, c, "map_type", "key_type"); ok {
				return t, true
			}
			return collectionType(workspace, c, "set_type", "")
		}
	case "bin_expression":
		// `value in c`
		if op := context.ChildByFieldName("op"); op != nil && op.Kind() == "in" && sameNode(context.ChildByFieldName("lhs"), node) {
			if c, ok := typeOf(workspace, doc, context.ChildByFieldName("rhs")); ok {
				return memberType(workspace, c)
			}
		}
	case "map_or_sequence_lvalue", "seq_access_expression":
		// `m[key]`
		container := context.ChildByFieldName("seq")
		if container == nil {
			container = context.NamedChild(0)
		}
		if !sameNode(container, node) {
			if c, ok := typeOf(workspace, doc, container); ok {
				return collectionType(workspace, c, "map_type", "key_type")
			}
		}
	}
	return typeRef{}, false
}

// collectionType returns a part of collection type t if t is of kind kind:
// the type in field, or the first type inside it if field is "".
func collectionType(workspace []*Document, t typeRef, kind, field string) (typeRef, bool) {
	u, ok := underlying(workspace, t)
	if !ok || u.node.Kind() != kind {
		return typeRef{}, false
	}
	if field == "" {
		return typeChild(u.doc, u.node)
	}
	if part := u.node.ChildByFieldName(field); part != nil {
		return typeRef{u.doc, part}, true
	}
	return typeRef{}, false
}

// memberType returns the type that `x in c` and `c -= x` expect x to have: a
// set's or sequence's element type, or a map's key type.
func memberType(workspace []*Document, c typeRef) (typeRef, bool) {
	if t, ok := collectionType(workspace, c, "map_type", "key_type"); ok {
		return t, true
	}
	if t, ok := collectionType(workspace, c, "set_type", ""); ok {
		return t, true
	}
	return collectionType(workspace, c, "sequence_type", "")
}

// tupleTypeElement returns the i-th element type of an unnamed tuple type.
func tupleTypeElement(workspace []*Document, t typeRef, i int) (typeRef, bool) {
	u, ok := underlying(workspace, t)
	if !ok || u.node.Kind() != "tuple_type" {
		return typeRef{}, false
	}
	elements := u.node.ChildrenByFieldName("tup_types", u.node.Walk())
	if i < 0 || i >= len(elements) {
		return typeRef{}, false
	}
	return typeRef{u.doc, &elements[i]}, true
}

// callParameters returns the names of a function's parameters, leaving out
// a foreign function's `return (result: T)`.
func callParameters(fn *sitter.Node) []*sitter.Node {
	if list := firstChildOfKind(fn, "function_param_list"); list != nil {
		return paramNames(list)
	}
	return nil
}

// argumentType returns the type expected of arg, one of the values in list:
// a function call's arguments, or the payload of send, raise, announce, goto
// or new.
func argumentType(workspace []*Document, doc *Document, list, arg *sitter.Node) (typeRef, bool) {
	i, n := indexOf(list, arg), int(list.NamedChildCount())
	statement := doc.parent(list)
	var payload typeRef
	var ok bool

	switch statement.Kind() {
	case "function_call_expression", "function_call_statement":
		fun := statement.ChildByFieldName("fun")
		if fun == nil {
			fun = statement.NamedChild(0)
		}
		if decls := resolve(workspace, doc, fun); len(decls) == 1 {
			if params := callParameters(decls[0].parent()); i < len(params) {
				return declaredType(declaration{doc: decls[0].doc, node: params[i]})
			}
		}
		return typeRef{}, false
	case "send_statement", "raise_statement", "announce_statement":
		// The event comes just before the payload: `send target, event,
		// payload`, `raise event, payload`. Counting named children instead
		// would miss a target like `this`, which has no named node.
		if i := indexOf(statement, list); i > 0 {
			payload, ok = eventPayload(workspace, doc, statement.NamedChild(uint(i-1)))
		}
	case "goto_statement":
		if name := firstChildOfKind(statement, "state_name"); name != nil {
			if decls := resolve(workspace, doc, name.ChildByFieldName("state")); len(decls) == 1 {
				payload, ok = entryPayload(workspace, decls[0].doc, decls[0].parent())
			}
		}
	case "ctor_expression", "ctor_statement":
		machine := statement.ChildByFieldName("interface_name")
		if machine == nil {
			machine = statement.NamedChild(0)
		}
		if decls := resolve(workspace, doc, machine); len(decls) == 1 {
			payload, ok = startPayload(workspace, decls[0])
		}
	}
	if !ok {
		return typeRef{}, false
	}
	return payloadElement(workspace, payload, i, n)
}

// payloadElement returns the type of the i-th of n values passed where
// payload is expected. A single value is the whole payload; several values
// fill a tuple payload's elements in order.
func payloadElement(workspace []*Document, payload typeRef, i, n int) (typeRef, bool) {
	if n == 1 {
		return payload, true
	}
	u, ok := underlying(workspace, payload)
	if !ok {
		return typeRef{}, false
	}
	switch u.node.Kind() {
	case "tuple_type":
		return tupleTypeElement(workspace, u, i)
	case "named_tuple_type":
		if list := firstChildOfKind(u.node, "identifier_type_list"); list != nil && i < int(list.NamedChildCount()) {
			return typeChild(u.doc, list.NamedChild(uint(i)))
		}
	}
	return typeRef{}, false
}

// eventPayload returns the payload type of the event an expression names.
func eventPayload(workspace []*Document, doc *Document, event *sitter.Node) (typeRef, bool) {
	if event == nil || event.Kind() != "identifier" {
		return typeRef{}, false
	}
	decls := resolve(workspace, doc, event)
	if len(decls) != 1 || decls[0].parent().Kind() != "event_declaration" {
		return typeRef{}, false
	}
	return typeChild(decls[0].doc, decls[0].parent())
}

// startPayload returns the entry parameter type of a machine's start state,
// which is what `new M(payload)` passes its payload to.
func startPayload(workspace []*Document, machine declaration) (typeRef, bool) {
	body := machineBody(machine.parent())
	if body == nil {
		return typeRef{}, false
	}
	for _, entry := range namedChildren(body) {
		state := entry.NamedChild(0)
		if state == nil || state.Kind() != "state_declaration" {
			continue
		}
		for i := uint(0); i < state.ChildCount(); i++ {
			if state.Child(i).Kind() == "start" {
				return entryPayload(workspace, machine.doc, state)
			}
		}
	}
	return typeRef{}, false
}

// entryPayload returns the parameter type of a state's entry handler: an
// anonymous `entry (p: T) { ... }` or a named `entry F;`.
func entryPayload(workspace []*Document, doc *Document, state *sitter.Node) (typeRef, bool) {
	if entry := firstChildOfKind(state, "state_entry_anon"); entry != nil {
		if handler := firstChildOfKind(entry, "anon_event_handler"); handler != nil {
			if param := firstChildOfKind(handler, "function_param"); param != nil {
				return typeChild(doc, param)
			}
		}
	}
	if entry := firstChildOfKind(state, "state_entry_fun"); entry != nil {
		if decls := resolve(workspace, doc, entry.ChildByFieldName("function_name")); len(decls) == 1 {
			if params := callParameters(decls[0].parent()); len(params) > 0 {
				return declaredType(declaration{doc: decls[0].doc, node: params[0]})
			}
		}
	}
	return typeRef{}, false
}

func indexOf(parent, child *sitter.Node) int {
	for i, c := range namedChildren(parent) {
		if sameNode(c, child) {
			return i
		}
	}
	return -1
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
	if found := named(fieldsOf(workspace, t), name); len(found) > 0 {
		return found[0], true
	}
	return declaration{}, false
}

// fieldsOf returns the fields of values of type t. A name can appear more
// than once, for an event set whose events share a field; the first one is
// the one the compiler uses.
func fieldsOf(workspace []*Document, t typeRef) []declaration {
	u, ok := underlying(workspace, t)
	if !ok {
		return nil
	}
	switch u.node.Kind() {
	case "named_tuple_type":
		return tupleFields(u)
	case "named_type":
		if typeName := u.node.ChildByFieldName("name"); typeName != nil {
			return verifierFields(workspace, typeName.Utf8Text(u.doc.Text))
		}
	}
	return nil
}

func tupleFields(tuple typeRef) []declaration {
	list := firstChildOfKind(tuple.node, "identifier_type_list")
	if list == nil {
		return nil
	}
	names := []*sitter.Node{}
	for _, field := range namedChildren(list) {
		names = appendName(names, field, "name")
	}
	return declarationsOf(tuple.doc, names)
}

// verifierFields returns the fields of a value whose type is the machine,
// interface, event or event set called typeName. The P verifier lets
// `n.voted` read machine n's variable voted, and `e.src` read field src of
// event e's payload.
func verifierFields(workspace []*Document, typeName string) []declaration {
	globals := globalDeclarationsIn(workspace, nsValue)
	fields := []declaration{}
	for _, d := range named(globals, typeName) {
		switch decl := d.parent(); decl.Kind() {
		case "impl_machine_declaration", "spec_machine_declaration", "scenario_machine_declaration":
			fields = append(fields, machineVariables(d.doc, decl)...)
		case "interface_declaration":
			// The compiler uses the machine with the interface's name.
			for _, m := range named(globals, typeName) {
				if machineBody(m.parent()) != nil {
					fields = append(fields, machineVariables(m.doc, m.parent())...)
				}
			}
		case "event_declaration":
			if payload, ok := typeChild(d.doc, decl); ok {
				fields = append(fields, fieldsOf(workspace, payload)...)
			}
		case "event_set_declaration":
			// The fields of each event in the set, in order.
			events := firstChildOfKind(decl, "event_set_literal")
			if events == nil {
				continue
			}
			for _, event := range events.ChildrenByFieldName("events", events.Walk()) {
				// Skipping the set's own name guards against a set that
				// lists itself, which the compiler would reject anyway.
				if eventName := event.Utf8Text(d.doc.Text); eventName != typeName {
					fields = append(fields, verifierFields(workspace, eventName)...)
				}
			}
		}
	}
	return fields
}

func machineVariable(doc *Document, machine *sitter.Node, name string) (declaration, bool) {
	if found := named(machineVariables(doc, machine), name); len(found) > 0 {
		return found[0], true
	}
	return declaration{}, false
}

// machineVariables returns a machine's variables, without its functions.
func machineVariables(doc *Document, machine *sitter.Node) []declaration {
	vars := []declaration{}
	for _, d := range declarationsOf(doc, machineMembers(machineBody(machine), nsValue)) {
		if d.parent().Kind() == "identifier_list" {
			vars = append(vars, d)
		}
	}
	return vars
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
			if d.parent().Kind() == "p_type_def" {
				aliases = append(aliases, d)
			}
		}
		switch len(aliases) {
		case 0:
			return t, true
		case 1:
			var ok bool
			if t, ok = typeChild(aliases[0].doc, aliases[0].parent()); !ok {
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
	decl := d.parent()
	if decl.Kind() == "identifier_list" {
		decl = d.doc.parent(decl)
	}
	switch decl.Kind() {
	case "var_declaration", "function_param", "identifier_type":
		return typeChild(d.doc, decl)
	}
	return typeRef{}, false
}

// returnType returns the return type of the function a declaration names.
func returnType(d declaration) (typeRef, bool) {
	fn := d.parent()
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
