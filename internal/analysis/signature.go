package analysis

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"

	protocol "github.com/tliron/glsp/protocol_3_16"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// SignatureHelp describes what's being called at pos: a function's
// parameters, a machine's start parameter after `new`, or an event's
// payload type in a `send`, `raise` or `announce`. The code being typed
// usually doesn't parse yet, so the call is found by scanning the text
// before pos, and only the names in it are looked up in the tree.
func SignatureHelp(workspace []*Document, doc *Document, pos protocol.Position) *protocol.SignatureHelp {
	offset := doc.offset(pos)
	c, ok := scanCall(doc, offset)
	if !ok {
		return nil
	}
	if c.callee != nil {
		return calleeSignature(workspace, doc, c)
	}
	return eventSignature(workspace, doc, c)
}

// call is what scanCall finds before the cursor.
type call struct {
	// callee is the name before the innermost unclosed `(`, if that's a
	// call, and argument is how many arguments come before the cursor.
	callee   *token
	isNew    bool
	argument int
	// statement is the tokens of the statement the cursor is in, up to
	// the cursor, for finding a send, raise or announce.
	statement []token
}

type token struct {
	text  string
	start uint
	// depth is how many parentheses enclose the token, within its
	// statement.
	depth int
}

// scanCall tokenizes the code from the start of the function body the
// cursor is in (or the file), up to the cursor, keeping track
// of unclosed parentheses and where the current statement began.
func scanCall(doc *Document, offset uint) (call, bool) {
	start := scanStart(doc, offset)
	type open struct {
		// callee is the index in tokens of the name before the `(`, or -1.
		callee, commas int
	}
	var tokens []token
	var opens []open
	statementStart := 0
	for _, t := range lex(doc.Text[start:offset], start) {
		switch t.text {
		case "(":
			callee := -1
			if n := len(tokens); n > 0 && isIdentifier(tokens[n-1].text) && !keywords[tokens[n-1].text] {
				callee = n - 1
			}
			opens = append(opens, open{callee: callee})
		case ")":
			if len(opens) > 0 {
				opens = opens[:len(opens)-1]
			}
		case ",":
			if len(opens) > 0 {
				opens[len(opens)-1].commas++
			}
		case ";", "{", "}":
			if len(opens) == 0 {
				t.depth = 0
				tokens = append(tokens, t)
				statementStart = len(tokens)
				continue
			}
		}
		t.depth = len(opens)
		tokens = append(tokens, t)
	}

	c := call{statement: tokens[statementStart:]}
	// The innermost unclosed `(` that's a call.
	for i := len(opens) - 1; i >= 0; i-- {
		if opens[i].callee >= 0 {
			callee := tokens[opens[i].callee]
			c.callee, c.argument = &callee, opens[i].commas
			c.isNew = opens[i].callee > 0 && tokens[opens[i].callee-1].text == "new"
			return c, true
		}
		// A tuple in the payload of a send, say: keep looking outward, for
		// a call or the statement.
	}
	return c, len(c.statement) > 0
}

// scanStart returns where scanCall starts: the function body around
// offset, or else the start of the file. Half-typed code often leaves no
// function body, or even declaration, around the cursor, and scanning
// from the start of the file costs little: statements end at `;`, `{` and
// `}`, wherever scanning starts.
func scanStart(doc *Document, offset uint) uint {
	at := offset
	if at > 0 {
		at--
	}
	for n := doc.Tree.RootNode().DescendantForByteRange(at, at); n != nil; n = n.Parent() {
		if n.Kind() == "function_body" {
			return n.StartByte()
		}
	}
	return 0
}

// lex splits P source into names, numbers, strings and single punctuation
// characters, dropping whitespace and comments. base is src's offset in
// its document.
func lex(src []byte, base uint) []token {
	var tokens []token
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(string(src[i+2:]), "*/")
			if end < 0 {
				return tokens
			}
			i += end + 4
		case c == '"':
			j := i + 1
			for j < len(src) && src[j] != '"' {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			tokens = append(tokens, token{text: string(src[i:min(j+1, len(src))]), start: base + uint(i)})
			i = j + 1
		case c == '_' || unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c)):
			j := i
			for j < len(src) && (src[j] == '_' || unicode.IsLetter(rune(src[j])) || unicode.IsDigit(rune(src[j]))) {
				j++
			}
			tokens = append(tokens, token{text: string(src[i:j]), start: base + uint(i)})
			i = j
		default:
			tokens = append(tokens, token{text: string(c), start: base + uint(i)})
			i++
		}
	}
	return tokens
}

func isIdentifier(s string) bool {
	return identifierPattern.MatchString(s)
}

// calleeSignature describes the function or machine before an open `(`.
func calleeSignature(workspace []*Document, doc *Document, c call) *protocol.SignatureHelp {
	name := c.callee.text
	var decl declaration
	found := false
	for _, d := range lookupName(workspace, doc, c.callee) {
		kind := d.node.Parent().Kind()
		if c.isNew && slices.Contains(machineKinds, kind) || !c.isNew && slices.Contains(functionKinds, kind) {
			decl, found = d, true
			break
		}
	}
	if !found {
		return nil
	}

	var label string
	var params []string
	switch fn := decl.node.Parent(); fn.Kind() {
	case "interface_declaration":
		label = "new " + name + "("
		if t, ok := typeChildText(decl.doc, fn); ok {
			label += t
			params = []string{t}
		}
		label += ")"
	case "impl_machine_declaration":
		label = "new " + name + "("
		if param := startParameter(decl.doc, fn); param != nil {
			p := strings.Join(strings.Fields(param.Utf8Text(decl.doc.Text)), " ")
			label += p
			params = []string{p}
		}
		label += ")"
	default:
		label = strings.Join(strings.Fields(strings.TrimSuffix(signature(decl), ";")), " ")
		for _, p := range parameters(fn) {
			if p.Parent().Kind() == "function_param" && p.Parent().Parent().Kind() == "function_param_list" {
				params = append(params, strings.Join(strings.Fields(p.Parent().Utf8Text(decl.doc.Text)), " "))
			}
		}
	}
	return signatureHelp(label, strings.Index(label, "("), params, c.argument, docComment(decl))
}

// lookupName resolves the name t, from the tree if it has an identifier
// there, and otherwise among the top-level declarations.
func lookupName(workspace []*Document, doc *Document, t *token) []declaration {
	n := doc.Tree.RootNode().NamedDescendantForByteRange(t.start, t.start+uint(len(t.text)))
	if n != nil && n.Kind() == "identifier" && n.StartByte() == t.start {
		if decls := resolve(workspace, doc, n); len(decls) > 0 {
			return decls
		}
		return named(namesInScope(workspace, doc, n), t.text)
	}
	return named(globalDeclarationsIn(workspace, nsValue), t.text)
}

// startParameter returns the parameter of a machine's start state's entry
// handler, as in `entry (config: tConfig)`, or nil if it has none.
func startParameter(doc *Document, machine *sitter.Node) *sitter.Node {
	body := machineBody(machine)
	if body == nil {
		return nil
	}
	for _, entry := range namedChildren(body) {
		state := firstChildOfKind(entry, "state_declaration")
		if state == nil || firstTokenOfKind(state, "start") == nil {
			continue
		}
		for _, item := range namedChildren(state) {
			if handler := firstChildOfKind(item, "anon_event_handler"); item.Kind() == "state_entry_anon" && handler != nil {
				return firstChildOfKind(handler, "function_param")
			}
		}
	}
	return nil
}

// eventSignature describes the payload of the event in a send, raise or
// announce statement, once the cursor is past the event.
func eventSignature(workspace []*Document, doc *Document, c call) *protocol.SignatureHelp {
	s := c.statement
	if len(s) == 0 {
		return nil
	}
	// The event is after this many top-level commas.
	eventAt := 0
	switch s[0].text {
	case "send":
		eventAt = 1
	case "raise", "announce":
	default:
		return nil
	}
	commas := 0
	var event *token
	for i := 1; i < len(s); i++ {
		if s[i].depth > 0 {
			continue
		}
		if s[i].text == "," {
			commas++
		} else if commas == eventAt && event == nil && isIdentifier(s[i].text) {
			event = &s[i]
		}
	}
	if commas <= eventAt || event == nil {
		return nil
	}
	for _, d := range lookupName(workspace, doc, event) {
		if d.node.Parent().Kind() != "event_declaration" {
			continue
		}
		payload, ok := typeChildText(d.doc, d.node.Parent())
		if !ok {
			payload = "no payload"
		}
		prefix := "event " + event.text + ": "
		return signatureHelp(prefix+payload, len(prefix), []string{payload}, 0, docComment(d))
	}
	return nil
}

// typeChildText returns the text of the type in a declaration, between
// its `:` (or `(`) and its end, for the primitive types that have no node.
func typeChildText(doc *Document, decl *sitter.Node) (string, bool) {
	switch decl.Kind() {
	case "event_declaration":
		colon, semicolon := firstTokenOfKind(decl, ":"), firstTokenOfKind(decl, ";")
		if colon == nil || semicolon == nil {
			return "", false
		}
		return strings.Join(strings.Fields(string(doc.Text[colon.EndByte():semicolon.StartByte()])), " "), true
	case "interface_declaration":
		open, close := firstTokenOfKind(decl, "("), firstTokenOfKind(decl, ")")
		if open == nil || close == nil || close.StartByte() == open.EndByte() {
			return "", false
		}
		return strings.Join(strings.Fields(string(doc.Text[open.EndByte():close.StartByte()])), " "), true
	}
	return "", false
}

// signatureHelp builds the response for one signature whose parameters
// appear in its label in order, from the byte offset from, with the active
// one highlighted.
func signatureHelp(label string, from int, params []string, active int, doc string) *protocol.SignatureHelp {
	info := protocol.SignatureInformation{Label: label}
	if doc != "" {
		info.Documentation = protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: doc}
	}
	from = max(from, 0)
	for _, p := range params {
		i := strings.Index(label[from:], p)
		if i < 0 {
			continue
		}
		start := utf16Len(label[:from+i])
		info.Parameters = append(info.Parameters, protocol.ParameterInformation{
			Label: []protocol.UInteger{start, start + utf16Len(p)},
		})
		from += i + len(p)
	}
	zero := protocol.UInteger(0)
	help := &protocol.SignatureHelp{Signatures: []protocol.SignatureInformation{info}, ActiveSignature: &zero}
	if len(info.Parameters) > 0 {
		a := protocol.UInteger(min(active, len(info.Parameters)-1))
		help.ActiveParameter = &a
	}
	return help
}

func utf16Len(s string) protocol.UInteger {
	return protocol.UInteger(len(utf16.Encode([]rune(s))))
}
