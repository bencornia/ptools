package source

import (
	"bytes"
	"encoding/json"
	"strconv"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// indexEnum records an enum's constants: numbered from 0 in order, unless
// they're given values.
func (x *Index) indexEnum(f *File, enum *sitter.Node) {
	next := 0
	walk(enum, func(n *sitter.Node) bool {
		switch n.Kind() {
		case "enum_elem":
			if name := n.ChildByFieldName("name"); name != nil {
				x.enums[name.Utf8Text(f.Text)] = next
				next++
			}
			return false
		case "numbered_enum_elem":
			name, value := n.ChildByFieldName("name"), n.ChildByFieldName("value")
			if name != nil && value != nil {
				if v, err := strconv.Atoi(value.Utf8Text(f.Text)); err == nil {
					x.enums[name.Utf8Text(f.Text)] = v
				}
			}
			return false
		}
		return true
	})
}

// PayloadAgrees compares the payload a send statement writes with the
// payload a trace says was sent, as PChecker writes it in JSON. It reports
// whether nothing the statement says contradicts the payload, and how many
// of its literal values, enum constants among them, the payload confirms.
// Values the statement computes, like variables, neither confirm nor
// contradict it.
func (x *Index) PayloadAgrees(send Node, payload json.RawMessage) (agrees bool, confirmed int) {
	// send target, event, payload...
	var values []*sitter.Node
	commas := 0
	for i := uint(0); i < send.ChildCount(); i++ {
		c := send.Child(i)
		if c.Kind() == "," {
			commas++
		} else if commas == 2 && c.Kind() == "rvalue_list" {
			for j := uint(0); j < c.NamedChildCount(); j++ {
				values = append(values, c.NamedChild(j))
			}
		}
	}
	switch len(values) {
	case 0:
		return true, 0
	case 1:
		return x.agrees(send, values[0], payload)
	}
	// send t, e, a, b sends the tuple (a, b).
	return x.agreesTuple(send, values, payload)
}

// unwrapped are the nodes that only wrap the value inside them.
var unwrapped = map[string]bool{
	"rvalue": true, "expression": true, "primitive_expression": true,
	"string_expression": true, "formatted_string": true, "paren_expression": true,
}

func (x *Index) agrees(in Node, value *sitter.Node, payload json.RawMessage) (bool, int) {
	for unwrapped[value.Kind()] && value.NamedChildCount() == 1 {
		value = value.NamedChild(0)
	}
	payload = bytes.TrimSpace(payload)
	switch value.Kind() {
	case "named_tuple_expression":
		var fields map[string]json.RawMessage
		if json.Unmarshal(payload, &fields) != nil {
			return true, 0
		}
		body := firstChild(value, "named_tuple_body")
		if body == nil {
			return true, 0
		}
		confirmed := 0
		// name = value, name = value...
		for i := uint(0); i+2 < body.ChildCount(); i++ {
			if body.Child(i).Kind() != "identifier" || body.Child(i+1).Kind() != "=" {
				continue
			}
			got, ok := fields[in.text(body.Child(i))]
			if !ok {
				continue
			}
			agrees, n := x.agrees(in, body.Child(i+2), got)
			if !agrees {
				return false, 0
			}
			confirmed += n
		}
		return true, confirmed
	case "unnamed_tuple_expression":
		body := firstChild(value, "unnamed_tuple_body")
		if body == nil {
			return true, 0
		}
		var values []*sitter.Node
		for i := uint(0); i < body.NamedChildCount(); i++ {
			values = append(values, body.NamedChild(i))
		}
		return x.agreesTuple(in, values, payload)
	case "int_literal", "unary_expression":
		want, err := strconv.Atoi(in.text(value))
		if err != nil {
			return true, 0
		}
		return agreesNumber(want, payload)
	case "identifier":
		want, ok := x.enums[in.text(value)]
		if !ok {
			return true, 0 // a variable
		}
		return agreesNumber(want, payload)
	case "bool_literal":
		var got bool
		if json.Unmarshal(payload, &got) != nil {
			return true, 0
		}
		return compared((in.text(value) == "true") == got)
	case "string_literal":
		want, err := strconv.Unquote(in.text(value))
		var got string
		if err != nil || json.Unmarshal(payload, &got) != nil {
			return true, 0
		}
		return compared(want == got)
	case "null_literal":
		return compared(string(payload) == "null" || string(payload) == `"null"`)
	}
	return true, 0
}

// agreesTuple compares an unnamed tuple's values with a payload, which
// PChecker writes as a list.
func (x *Index) agreesTuple(in Node, values []*sitter.Node, payload json.RawMessage) (bool, int) {
	var got []json.RawMessage
	if json.Unmarshal(payload, &got) != nil || len(got) != len(values) {
		return true, 0
	}
	confirmed := 0
	for i, v := range values {
		agrees, n := x.agrees(in, v, got[i])
		if !agrees {
			return false, 0
		}
		confirmed += n
	}
	return true, confirmed
}

func agreesNumber(want int, payload json.RawMessage) (bool, int) {
	var got float64
	if json.Unmarshal(payload, &got) != nil {
		return true, 0
	}
	return compared(float64(want) == got)
}

// compared is the outcome of comparing a literal value: it agrees, and
// confirms one value, or it doesn't.
func compared(equal bool) (bool, int) {
	if equal {
		return true, 1
	}
	return false, 0
}
