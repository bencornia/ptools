package analysis

import (
	protocol "github.com/tliron/glsp/protocol_3_16"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Semantic token types and modifiers, in the order of the legend the server
// advertises; tokens refer to them by index.
const (
	tokenNamespace = iota
	tokenType
	tokenClass
	tokenEnum
	tokenInterface
	tokenParameter
	tokenVariable
	tokenProperty
	tokenEnumMember
	tokenEvent
	tokenFunction
	tokenKeyword
	tokenComment
	tokenString
	tokenNumber
)

const modifierDeclaration = 1 << 0

// TokenLegend is the legend for SemanticTokens' token types and modifiers.
var TokenLegend = protocol.SemanticTokensLegend{
	TokenTypes: []string{
		"namespace", "type", "class", "enum", "interface", "parameter", "variable", "property",
		"enumMember", "event", "function", "keyword", "comment", "string", "number",
	},
	TokenModifiers: []string{"declaration"},
}

// declarationTokens maps the symbol kinds of declarations to token types.
// States, modules, tests and proofs are namespaces, as in the outline.
var declarationTokens = map[protocol.SymbolKind]int{
	protocol.SymbolKindStruct:     tokenType,
	protocol.SymbolKindEnum:       tokenEnum,
	protocol.SymbolKindEnumMember: tokenEnumMember,
	protocol.SymbolKindEvent:      tokenEvent,
	protocol.SymbolKindInterface:  tokenInterface,
	protocol.SymbolKindClass:      tokenClass,
	protocol.SymbolKindModule:     tokenNamespace,
	protocol.SymbolKindFunction:   tokenFunction,
	protocol.SymbolKindNamespace:  tokenNamespace,
	protocol.SymbolKindProperty:   tokenProperty,
}

// builtinTypes are keywords that name types. `machine` and `event` also
// start declarations, so they're types only where a type is written.
var builtinTypes = map[string]bool{
	"any": true, "bool": true, "data": true, "float": true, "int": true, "string": true,
	"map": true, "seq": true, "set": true, "machine": true, "event": true,
}

// SemanticTokens returns doc's tokens, encoded as LSP expects, within rng,
// or in the whole document if rng is nil. Names are classified by what
// they refer to, so an event and a machine look different wherever they're
// used; names that don't resolve are left to the editor's own highlighting.
func SemanticTokens(workspace []*Document, doc *Document, rng *protocol.Range) []protocol.UInteger {
	var start, end uint
	if rng != nil {
		start, end = doc.offset(rng.Start), doc.offset(rng.End)
	} else {
		start, end = 0, uint(len(doc.Text))
	}

	e := tokenEncoder{doc: doc}
	classify(workspace, doc, start, end, e.add)
	return e.data
}

// Token is a classified span of a document, in bytes, as Highlight
// returns it.
type Token struct {
	Start       uint   `json:"start"`
	End         uint   `json:"end"`
	Type        string `json:"type"`
	Declaration bool   `json:"declaration,omitempty"`
}

// Highlight returns doc's tokens in document order, classified as
// SemanticTokens classifies them, with TokenLegend's names. Unlike
// SemanticTokens, a token may span lines. Names that don't resolve are left
// out, as is punctuation.
func Highlight(workspace []*Document, doc *Document) []Token {
	tokens := []Token{}
	classify(workspace, doc, 0, uint(len(doc.Text)), func(n *sitter.Node, token, modifiers int) {
		tokens = append(tokens, Token{
			Start:       n.StartByte(),
			End:         n.EndByte(),
			Type:        TokenLegend.TokenTypes[token],
			Declaration: modifiers&modifierDeclaration != 0,
		})
	})
	return tokens
}

// classify calls emit, in document order, for each token of doc that
// overlaps the bytes from start to end.
func classify(workspace []*Document, doc *Document, start, end uint, emit func(n *sitter.Node, token, modifiers int)) {
	doc.walk(func(n *sitter.Node) bool {
		if n.EndByte() <= start || n.StartByte() >= end {
			return false
		}
		switch kind := n.Kind(); {
		case kind == "comment":
			emit(n, tokenComment, 0)
		case kind == "string_literal":
			emit(n, tokenString, 0)
		case kind == "int_literal" || kind == "float_literal":
			// A float's parts are int_literals and a '.'.
			emit(n, tokenNumber, 0)
		case kind == "identifier":
			if token, modifiers, ok := identifierToken(workspace, doc, n); ok {
				emit(n, token, modifiers)
			}
		case n.ChildCount() == 0 && !n.IsNamed() && keywords[kind]:
			if builtinTypes[kind] && isTypePosition(doc, n) {
				emit(n, tokenType, 0)
			} else {
				emit(n, tokenKeyword, 0)
			}
		default:
			return true
		}
		return false
	})
}

// identifierToken classifies a name by the declaration it refers to.
func identifierToken(workspace []*Document, doc *Document, ident *sitter.Node) (token, modifiers int, ok bool) {
	decls := resolve(workspace, doc, ident)
	if len(decls) == 0 {
		return 0, 0, false
	}
	d := decls[0]
	if sameNode(d.node, ident) {
		modifiers = modifierDeclaration
	}

	switch decl := d.parent(); decl.Kind() {
	case "identifier_list":
		// `var` in a machine, function or handler, or a global `param`.
		if owner := d.doc.parent(decl); d.doc.parent(owner) != nil && d.doc.parent(owner).Kind() == "machine_entry" {
			return tokenProperty, modifiers, true
		}
		return tokenVariable, modifiers, true
	case "function_param":
		return tokenParameter, modifiers, true
	case "identifier_type":
		return tokenProperty, modifiers, true
	default:
		if spec, ok := symbolSpecs[decl.Kind()]; ok {
			if token, ok := declarationTokens[spec.kind]; ok {
				return token, modifiers, true
			}
		}
	}
	return 0, 0, false
}

// isTypePosition reports whether a builtin type keyword is used as a type,
// as in `var m: machine` or `map[int, machine]`, rather than starting a
// declaration such as `machine M {`.
func isTypePosition(doc *Document, keyword *sitter.Node) bool {
	if typeKinds[doc.parent(keyword).Kind()] {
		return true
	}
	prev := keyword.PrevSibling()
	return prev != nil && (prev.Kind() == ":" || prev.Kind() == "as")
}

// tokenEncoder builds the LSP encoding of semantic tokens: five integers per
// token, its position relative to the previous token's, its length, type
// and modifiers. Tokens must be added in document order.
type tokenEncoder struct {
	doc                *Document
	data               []protocol.UInteger
	prevLine, prevChar protocol.UInteger
}

func (e *tokenEncoder) add(n *sitter.Node, token, modifiers int) {
	// Tokens can't span lines, so a multi-line comment or string becomes
	// one token per line.
	start, end := n.StartPosition(), n.EndPosition()
	for row := start.Row; row <= end.Row; row++ {
		from := sitter.Point{Row: row}
		if row == start.Row {
			from.Column = start.Column
		}
		to := end
		if row < end.Row {
			to = sitter.Point{Row: row, Column: e.doc.lineLength(row)}
		}
		e.addSpan(e.doc.Position(from), e.doc.Position(to), token, modifiers)
	}
}

func (e *tokenEncoder) addSpan(from, to protocol.Position, token, modifiers int) {
	if to.Character <= from.Character {
		return
	}
	deltaChar := from.Character
	if from.Line == e.prevLine {
		deltaChar -= e.prevChar
	}
	e.data = append(e.data, from.Line-e.prevLine, deltaChar, to.Character-from.Character,
		protocol.UInteger(token), protocol.UInteger(modifiers))
	e.prevLine, e.prevChar = from.Line, from.Character
}
