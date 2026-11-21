// Package version holds the ptools release, which every command reports.
package version

// Version is the ptools release, as tagged in git without its "v".
const Version = "0.7.0"

// Grammar is the tree-sitter-p commit that go.mod requires, in full, for
// pinning editors' builds of the grammar to the one plsp parses with.
// TestGrammar checks that it matches go.mod.
const Grammar = "6057d17685c680544631ad0e0b43688ab870e4cd"
