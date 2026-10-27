package analysis

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// keywords are the P lexer's keywords, which can't be used as names.
var keywords = map[string]bool{}

func init() {
	for _, k := range strings.Fields(`Lemma Proof Theorem announce any as assert assume axiom bool break
		case choose cold compose continue creates data default defer do else ensures entry enum
		event eventset except exists exit false float forall foreach format fun goto halt hidee
		hidei hot if ignore implementation in inflight int interface invariant is keys machine
		main map module new null observes on pairwise param paramtest print prove pure raise
		receive receives refines rename requires return safe scenario send sends sent seq set
		sizeof spec start state string targets test this to true type union using values var
		while wise with`) {
		keywords[k] = true
	}
}

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// PrepareRename returns the range of the name at pos if it can be renamed:
// it's an identifier that resolves to a declaration.
func PrepareRename(workspace []*Document, doc *Document, pos protocol.Position) *protocol.Range {
	ident := identifierAt(doc, pos)
	if ident == nil || len(resolve(workspace, doc, ident)) == 0 {
		return nil
	}
	rng := doc.Range(ident)
	return &rng
}

// renaming is one declaration to rename: what its references and its own
// name become, which differ for invariants in a Lemma or Theorem.
type renaming struct {
	target             declaration
	newRef, newDecl    string
	includeDeclaration bool
}

// Rename returns the edits, by document URI, that rename the declaration the
// identifier at pos refers to, and every reference to it, to newName. It
// refuses renames that would change what other names refer to.
func Rename(candidates []*Document, workspaceOf func(*Document) []*Document, doc *Document, pos protocol.Position, newName string) (map[string][]protocol.TextEdit, error) {
	ident := identifierAt(doc, pos)
	if ident == nil {
		return nil, fmt.Errorf("there's no name here to rename")
	}
	oldName := ident.Utf8Text(doc.Text)
	targets := resolve(workspaceOf(doc), doc, ident)
	if len(targets) == 0 {
		return nil, fmt.Errorf("can't find where %s is declared", oldName)
	}
	if err := checkName(newName); err != nil {
		return nil, err
	}
	if newName == oldName {
		return map[string][]protocol.TextEdit{}, nil
	}

	var renamings []renaming
	for _, t := range targets {
		rs, err := renamingsFor(t, ident.Utf8Text(doc.Text), newName)
		if err != nil {
			return nil, err
		}
		renamings = append(renamings, rs...)
	}

	for _, r := range renamings {
		if err := checkConflicts(candidates, workspaceOf, r); err != nil {
			return nil, err
		}
	}

	edits := map[string][]protocol.TextEdit{}
	seen := map[nodeKey]bool{}
	for _, r := range renamings {
		for _, ref := range referencesTo(candidates, workspaceOf, []declaration{r.target}, r.includeDeclaration) {
			if k := keyOf(ref.doc, ref.node); !seen[k] {
				seen[k] = true
				text := r.newRef
				if sameNode(ref.node, r.target.node) && ref.doc == r.target.doc {
					text = r.newDecl
				}
				edits[ref.doc.URI] = append(edits[ref.doc.URI], protocol.TextEdit{Range: ref.doc.Range(ref.node), NewText: text})
			}
		}
	}
	for _, list := range edits {
		slices.SortFunc(list, func(a, b protocol.TextEdit) int {
			return cmp.Or(
				cmp.Compare(a.Range.Start.Line, b.Range.Start.Line),
				cmp.Compare(a.Range.Start.Character, b.Range.Start.Character),
			)
		})
	}
	return edits, nil
}

func checkName(name string) error {
	if !identifierPattern.MatchString(name) {
		return fmt.Errorf("%q isn't a valid P name", name)
	}
	if keywords[name] {
		return fmt.Errorf("%q is a P keyword", name)
	}
	return nil
}

// renamingsFor works out what renaming t to newName changes. cursorText is
// the name as written where the rename was asked for.
func renamingsFor(t declaration, cursorText, newName string) ([]renaming, error) {
	declText := t.node.Utf8Text(t.doc.Text)

	// An invariant in a Lemma or Theorem: declared as `name`, referenced as
	// `group_name`.
	if t.name != declText {
		prefix := strings.TrimSuffix(t.name, declText)
		member := newName
		if cursorText == t.name {
			// Renamed from a reference, so newName includes the prefix.
			var ok bool
			if member, ok = strings.CutPrefix(newName, prefix); !ok || member == "" {
				return nil, fmt.Errorf("%s belongs to %s, so its new name must start with %q; rename %s to change that",
					t.name, strings.TrimSuffix(prefix, "_"), prefix, strings.TrimSuffix(prefix, "_"))
			}
		}
		return []renaming{{target: t, newRef: prefix + member, newDecl: member, includeDeclaration: true}}, nil
	}

	renamings := []renaming{{target: t, newRef: newName, newDecl: newName, includeDeclaration: true}}
	// A Lemma or Theorem: references to its invariants spell its name too.
	if group := t.node.Parent(); group.Kind() == "invariant_group_declaration" {
		for _, member := range invariantGroupDeclarations(t.doc, group)[1:] {
			memberText := member.node.Utf8Text(member.doc.Text)
			renamings = append(renamings, renaming{target: member, newRef: newName + "_" + memberText})
		}
	}
	return renamings, nil
}

// checkConflicts refuses a renaming that would collide with another
// declaration in the same scope, or change what some name refers to.
func checkConflicts(candidates []*Document, workspaceOf func(*Document) []*Document, r renaming) error {
	t := r.target
	workspace := workspaceOf(t.doc)
	ns := namespaceOf(t.node)

	// A tuple field only has to differ from the other fields of its tuple.
	if field := t.node.Parent(); field.Kind() == "identifier_type" {
		for _, sibling := range namedChildren(field.Parent()) {
			if name := sibling.ChildByFieldName("name"); name != nil && !sameNode(name, t.node) && name.Utf8Text(t.doc.Text) == r.newDecl {
				return fmt.Errorf("this tuple already has a field named %s", r.newDecl)
			}
		}
		return nil
	}

	scope := declaringScope(t, ns)
	if scope == nil {
		// A top-level declaration.
		if others := named(globalDeclarationsIn(workspace, ns), r.newRef); len(others) > 0 {
			return alreadyDeclared(r.newRef, others[0])
		}
	} else {
		for _, other := range declarationsOf(t.doc, scopeDeclarations(scope, ns)) {
			if other.name == r.newDecl && !sameNode(other.node, t.node) {
				return alreadyDeclared(r.newDecl, other)
			}
		}
	}
	if ns == nsModule || r.newRef != r.newDecl {
		// Modules and grouped invariants are only ever global.
		return nil
	}

	// A reference captured by a closer declaration of the new name, as when
	// a function has its own local with that name.
	for _, ref := range referencesTo(candidates, workspaceOf, []declaration{t}, false) {
		for s := ref.node.Parent(); s != nil; s = s.Parent() {
			decls := declarationsOf(ref.doc, scopeDeclarations(s, ns))
			if slices.ContainsFunc(decls, func(d declaration) bool { return sameNode(d.node, t.node) }) {
				break
			}
			if others := named(decls, r.newRef); len(others) > 0 {
				return fmt.Errorf("renaming to %s would make the reference on line %d of %s refer to a different %s",
					r.newRef, ref.node.StartPosition().Row+1, baseName(ref.doc.URI), r.newRef)
			}
		}
	}

	// A use of another declaration with the new name, captured by this one.
	if scope != nil {
		var captured *sitter.Node
		forEachIdentifier(scope, func(n *sitter.Node) {
			// Tuple fields and names in other namespaces can't be captured.
			if captured != nil || n.Utf8Text(t.doc.Text) != r.newRef || isFieldName(n) || namespaceOf(n) != ns {
				return
			}
			for _, d := range resolve(workspace, t.doc, n) {
				if d.doc != t.doc || !isWithin(d.node, scope) {
					captured = n
				}
			}
		})
		if captured != nil {
			return fmt.Errorf("renaming to %s would make the %s on line %d refer to this instead",
				r.newRef, r.newRef, captured.StartPosition().Row+1)
		}
	}
	return nil
}

// declaringScope returns the scope node that declares t (a function,
// handler, quantifier or machine), or nil for a top-level declaration.
func declaringScope(t declaration, ns namespace) *sitter.Node {
	for s := t.node.Parent(); s != nil; s = s.Parent() {
		for _, n := range scopeDeclarations(s, ns) {
			if sameNode(n, t.node) {
				return s
			}
		}
	}
	return nil
}

func isWithin(node, ancestor *sitter.Node) bool {
	for n := node; n != nil; n = n.Parent() {
		if sameNode(n, ancestor) {
			return true
		}
	}
	return false
}

func alreadyDeclared(name string, other declaration) error {
	return fmt.Errorf("%s is already declared on line %d of %s", name, other.node.StartPosition().Row+1, baseName(other.doc.URI))
}

func baseName(uri string) string {
	return uri[strings.LastIndex(uri, "/")+1:]
}
