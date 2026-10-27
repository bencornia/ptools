package analysis

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// WorkspaceSymbols returns the declarations in docs whose names match query,
// best matches first, at most limit of them. Matching ignores case and
// accepts the query's letters in order with gaps, so "wdr" finds
// "eWithDrawReq".
func WorkspaceSymbols(docs []*Document, query string, limit int) []protocol.SymbolInformation {
	type match struct {
		symbol protocol.SymbolInformation
		score  int
	}
	var matches []match
	for _, doc := range docs {
		var visit func(symbols []protocol.DocumentSymbol, container string)
		visit = func(symbols []protocol.DocumentSymbol, container string) {
			for _, s := range symbols {
				if isHandler(s) {
					continue
				}
				if score, ok := matchScore(query, s.Name); ok {
					info := protocol.SymbolInformation{
						Name:     s.Name,
						Kind:     s.Kind,
						Location: protocol.Location{URI: doc.URI, Range: s.SelectionRange},
					}
					if container != "" {
						info.ContainerName = &container
					}
					matches = append(matches, match{info, score})
				}
				// A function's children are its local variables.
				if s.Kind != protocol.SymbolKindFunction {
					visit(s.Children, s.Name)
				}
			}
		}
		visit(Symbols(doc), "")
	}

	slices.SortFunc(matches, func(a, b match) int {
		return cmp.Or(
			cmp.Compare(a.score, b.score),
			cmp.Compare(len(a.symbol.Name), len(b.symbol.Name)),
			cmp.Compare(a.symbol.Name, b.symbol.Name),
			cmp.Compare(a.symbol.Location.URI, b.symbol.Location.URI),
			cmp.Compare(a.symbol.Location.Range.Start.Line, b.symbol.Location.Range.Start.Line),
		)
	})
	symbols := make([]protocol.SymbolInformation, 0, min(len(matches), limit))
	for _, m := range matches[:min(len(matches), limit)] {
		symbols = append(symbols, m.symbol)
	}
	return symbols
}

// isHandler reports whether s is an entry, exit or `on e` handler in the
// outline, which has no name of its own to search for.
func isHandler(s protocol.DocumentSymbol) bool {
	return s.Kind == protocol.SymbolKindFunction && s.Detail == nil
}

// matchScore says how well name matches query, lower being better: an exact
// match, a prefix, a substring, the query's letters in order at the starts
// of words (as "wdr" in eWithDrawReq), or anywhere in order (as in
// newLeader). ok is false if it doesn't match at all. Case is ignored, and an
// empty query matches everything.
func matchScore(query, name string) (score int, ok bool) {
	q, n := strings.ToLower(query), strings.ToLower(name)
	switch {
	case q == n:
		return 0, true
	case strings.HasPrefix(n, q):
		return 1, true
	case strings.Contains(n, q):
		return 2, true
	case isSubsequence(q, strings.ToLower(wordStarts(name))):
		return 3, true
	case isSubsequence(q, n):
		return 4, true
	}
	return 0, false
}

// wordStarts returns the first letter of each word in a camelCase or
// snake_case name: its first letter, every capital, and every letter after
// an underscore or digit.
func wordStarts(name string) string {
	var b strings.Builder
	prev := '_'
	for _, r := range name {
		if unicode.IsLetter(r) && (prev == '_' || unicode.IsDigit(prev) || unicode.IsUpper(r)) {
			b.WriteRune(r)
		}
		prev = r
	}
	return b.String()
}

func isSubsequence(sub, s string) bool {
	for _, r := range sub {
		i := strings.IndexRune(s, r)
		if i < 0 {
			return false
		}
		s = s[i+utf8.RuneLen(r):]
	}
	return true
}
