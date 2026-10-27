// Package format formats P source code.
//
// It keeps the code's tokens, their order, the line breaks between them, and
// comments, and normalizes the whitespace: indentation, the spacing between
// tokens on a line, blank lines, and the end of the file. Like gofmt, it
// refuses code with syntax errors, and it checks its own output: if
// formatting would change anything but whitespace, it returns an error.
package format

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	tree_sitter_p "github.com/bencornia/tree-sitter-p/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Options controls formatting.
type Options struct {
	// Indent is the number of spaces per indentation level; 0 means 4.
	Indent int
}

// ErrSyntax is returned for source with syntax errors, which isn't
// formatted.
var ErrSyntax = errors.New("syntax error")

// Source formats P source code.
func Source(src []byte, opts Options) ([]byte, error) {
	if opts.Indent <= 0 {
		opts.Indent = 4
	}
	parser := newParser()
	defer parser.Close()

	tree := parser.Parse(src, nil)
	defer tree.Close()
	if tree.RootNode().HasError() {
		return nil, ErrSyntax
	}
	tokens, err := tokenize(src, tree)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return []byte{}, nil
	}
	out := render(tokens, strings.Repeat(" ", opts.Indent))

	// The output must be the same code: the same tokens, without errors.
	check := parser.Parse(out, nil)
	defer check.Close()
	if check.RootNode().HasError() {
		return nil, fmt.Errorf("formatting would introduce a syntax error; this is a bug in pfmt")
	}
	formatted, err := tokenize(out, check)
	if err != nil || !sameTokens(tokens, formatted) {
		return nil, fmt.Errorf("formatting would change more than whitespace; this is a bug in pfmt")
	}
	return out, nil
}

func newParser() *sitter.Parser {
	parser := sitter.NewParser()
	if err := parser.SetLanguage(sitter.NewLanguage(tree_sitter_p.Language())); err != nil {
		// Only happens if the grammar's ABI doesn't match the runtime,
		// which is a build problem.
		panic(err)
	}
	return parser
}

// token is a leaf of the syntax tree: a name, keyword, literal,
// punctuation, or comment.
type token struct {
	text string
	// kind is the node's kind, and parent its parent's, which some spacing
	// rules depend on.
	kind, parent string
	named        bool
	// newlines is how many line breaks came before the token, and spaces
	// how many spaces, if it was on the same line as the token before.
	newlines, spaces int
}

// tokenize returns the leaves of tree in order, with the whitespace between
// them. It checks that the leaves and whitespace account for all of src.
func tokenize(src []byte, tree *sitter.Tree) ([]token, error) {
	var tokens []token
	pos := uint(0)
	var walk func(n *sitter.Node, parent string) error
	walk = func(n *sitter.Node, parent string) error {
		if n.ChildCount() == 0 {
			if n.EndByte() == n.StartByte() {
				return nil
			}
			gap := src[pos:n.StartByte()]
			if len(bytes.TrimSpace(gap)) > 0 {
				return fmt.Errorf("unexpected %q between tokens; this is a bug in pfmt", gap)
			}
			newlines := bytes.Count(gap, []byte("\n"))
			spaces := len(gap)
			if newlines > 0 {
				spaces = 0
			}
			text := string(src[n.StartByte():n.EndByte()])
			if n.Kind() == "comment" && strings.HasPrefix(text, "//") {
				text = strings.TrimRight(text, " \t\r")
			}
			tokens = append(tokens, token{
				text: text, kind: n.Kind(), parent: parent, named: n.IsNamed(),
				newlines: newlines, spaces: spaces,
			})
			pos = n.EndByte()
			return nil
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			if err := walk(n.Child(i), n.Kind()); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(tree.RootNode(), ""); err != nil {
		return nil, err
	}
	if rest := src[pos:]; len(bytes.TrimSpace(rest)) > 0 {
		return nil, fmt.Errorf("unexpected %q at the end; this is a bug in pfmt", rest)
	}
	return tokens, nil
}

func sameTokens(a, b []token) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].text != b[i].text {
			return false
		}
	}
	return true
}

// open is a bracket that hasn't been closed yet.
type open struct {
	bracket string
	// indent is the indentation level of the line it's on.
	indent int
}

var closing = map[string]string{"}": "{", ")": "(", "]": "["}

// render writes the tokens with normalized whitespace.
func render(tokens []token, unit string) []byte {
	var out bytes.Buffer
	var stack []open
	level := 0                // the current line's indentation level
	var prev, prevCode *token // prevCode skips comments

	for i := range tokens {
		t := &tokens[i]
		popped := false
		switch {
		case prev == nil:
		case t.newlines > 0:
			out.WriteString(strings.Repeat("\n", min(t.newlines, 2)))
			level, popped = lineIndent(t, prevCode, &stack)
			out.WriteString(strings.Repeat(unit, level))
		case t.kind == "comment":
			// Trailing comments keep their alignment.
			out.WriteString(strings.Repeat(" ", max(t.spaces, 1)))
		case space(prev, t):
			out.WriteByte(' ')
		}
		out.WriteString(t.text)

		if !t.named {
			switch t.text {
			case "{", "(", "[":
				stack = append(stack, open{t.text, level})
			case "}", ")", "]":
				if !popped && len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
			}
		}
		prev = t
		if t.kind != "comment" {
			prevCode = t
		}
	}
	out.WriteByte('\n')
	return out.Bytes()
}

// lineIndent returns the indentation level of a line starting with t, and
// whether t is a closing bracket it took off the stack. A line inside
// brackets is one level deeper than the line the innermost one opened on,
// and a closing bracket lines up with that line. In a block, a line that
// continues a statement, rather than starting one, is one level deeper
// still.
func lineIndent(t, prevCode *token, stack *[]open) (int, bool) {
	n := len(*stack)
	if opener, ok := closing[t.text]; ok && !t.named && n > 0 && (*stack)[n-1].bracket == opener {
		top := (*stack)[n-1]
		*stack = (*stack)[:n-1]
		return top.indent, true
	}
	level := 0
	inBlock := true
	if n > 0 {
		level = (*stack)[n-1].indent + 1
		inBlock = (*stack)[n-1].bracket == "{"
	}
	if inBlock && t.text != "{" && !startsStatement(prevCode) {
		level++
	}
	return level, false
}

// startsStatement reports whether the token after prev starts a statement
// or declaration, rather than continuing one.
func startsStatement(prev *token) bool {
	if prev == nil {
		return true
	}
	switch prev.text {
	case ";", "{", "}":
		return !prev.named
	}
	return false
}

// Tokens that are spaced like binary operators.
var operators = map[string]bool{
	"=": true, "+=": true, "-=": true, "==": true, "!=": true, "<": true, "<=": true,
	">": true, ">=": true, "+": true, "-": true, "*": true, "/": true, "%": true,
	"&&": true, "||": true, "==>": true, "<==>": true, "->": true,
}

// Keywords that are called like functions, as in `sizeof(s)`.
var functionKeywords = map[string]bool{
	"keys": true, "values": true, "sizeof": true, "default": true, "choose": true,
	"format": true, "float": true,
}

// Keywords for types with a `[`, as in `map[int, int]`.
var typeKeywords = map[string]bool{"map": true, "seq": true, "set": true}

// Declarations whose `[main=M]` brackets keep their original spacing.
var mainBrackets = map[string]bool{
	"safety_test_declaration": true, "refinement_test_declaration": true, "implementation_declaration": true,
}

// space reports whether a space goes between two tokens on the same line.
func space(prev, t *token) bool {
	original := t.spaces > 0
	switch {
	case prev.kind == "comment":
		// After a /* comment */ on the same line.
		return true

	case isWord(prev) && isWord(t):
		return true

	case isUnary(prev):
		return false
	case operators[t.text] && !t.named, operators[prev.text] && !prev.named:
		if mainBrackets[t.parent] || mainBrackets[prev.parent] {
			// `[main=M]`
			return original
		}
		return true

	case t.text == "," || t.text == ";" || t.text == ")" || t.text == "]":
		return false
	case prev.text == "," || prev.text == ";":
		return true
	case prev.text == "(" || prev.text == "[":
		return false
	case t.text == "." || prev.text == ".":
		return false

	case t.text == ":":
		// `x: int` and `case e:`, but `forall (x: T) :: body`.
		return prev.text != ":" && t.parent == "quant_expression"
	case prev.text == ":":
		return true

	case t.text == "(":
		switch {
		case functionKeywords[prev.text]:
			return false
		case prev.kind == "identifier", prev.text == ")", prev.text == "]":
			// A call or a declaration's parameters.
			return false
		case isWord(prev):
			return true
		}
		return original
	case t.text == "[":
		// Indexing, `map[`, `seq[` and `set[`, but `test t [main=M]` and
		// `x in [1, 2]`.
		switch {
		case mainBrackets[t.parent]:
			return true
		case prev.kind == "identifier", prev.text == ")", prev.text == "]", typeKeywords[prev.text]:
			return false
		}
		return isWord(prev) || original

	case t.text == "{":
		return true
	case prev.text == "{":
		return t.text != "}"
	case t.text == "}":
		return true
	}
	return original
}

// isWord reports whether t is a name, keyword, or literal, which need a
// space between them.
func isWord(t *token) bool {
	if t.text == "" {
		return false
	}
	c := t.text[0]
	return c == '_' || c == '"' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// isUnary reports whether t is a unary `-` or `!`, which its operand
// follows directly.
func isUnary(t *token) bool {
	return (t.text == "-" || t.text == "!") && t.parent == "unary_expression"
}
