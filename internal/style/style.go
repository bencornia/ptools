// Package style holds the naming conventions plsp checks P names against,
// and which of its other checks are on, and reads them from a project's
// .plint.toml.
package style

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
)

// FileName is the configuration file's name. The nearest one in a P file's
// directory or above it applies to the file.
const FileName = ".plint.toml"

// Case is a way of writing a name.
type Case string

const (
	TitleCase     Case = "TitleCase"
	CamelCase     Case = "camelCase"
	SnakeCase     Case = "snake_case"
	ScreamingCase Case = "SCREAMING_CASE"
	// Off checks nothing.
	Off Case = "off"
)

var patterns = map[Case]*regexp.Regexp{
	TitleCase:     regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`),
	CamelCase:     regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`),
	SnakeCase:     regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`),
	ScreamingCase: regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)*$`),
}

// Naming is the case each kind of declaration's name is written in.
// Prefixes are the Hungarian prefixes a name may start with in any of them,
// as in tRequest or eResponse: a prefix followed by a TitleCase name.
type Naming struct {
	Types         Case     `toml:"types"`
	EnumConstants Case     `toml:"enum_constants"`
	Events        Case     `toml:"events"`
	Machines      Case     `toml:"machines"`
	States        Case     `toml:"states"`
	Modules       Case     `toml:"modules"`
	Functions     Case     `toml:"functions"`
	Variables     Case     `toml:"variables"`
	TestCases     Case     `toml:"test_cases"`
	Prefixes      []string `toml:"prefixes"`
}

// Level is how a check reports what it finds.
type Level string

const (
	LevelWarning Level = "warning"
	LevelError   Level = "error"
	LevelOff     Level = "off"
)

// Checks is the level of each check besides naming.
type Checks struct {
	// Unused declarations: types, events, functions and variables.
	Unused Level `toml:"unused"`
	// NeverSent events, which are handled or observed but never sent,
	// raised or announced.
	NeverSent Level `toml:"never_sent"`
	// UnreachableStates, which nothing goes to from the start state.
	UnreachableStates Level `toml:"unreachable_states"`
	// UnreachableCode, after a return, raise, goto, break or continue.
	UnreachableCode Level `toml:"unreachable_code"`
	// Shadowing of a machine variable by a parameter or local variable.
	Shadowing Level `toml:"shadowing"`
	// Order of a machine's states, the start state first, and of a
	// state's handlers: entry, then exit, then the rest.
	Order Level `toml:"order"`
}

// Config is a .plint.toml file.
type Config struct {
	Naming Naming `toml:"naming"`
	Checks Checks `toml:"checks"`
}

// Default is the configuration with no .plint.toml: types, and the
// declarations that are like types, in TitleCase, enum constants in
// SCREAMING_CASE, and everything else in snake_case. The prefixes are the
// ones P's own examples use, for types, events and test cases. Every other
// check warns.
func Default() Config {
	return Config{Checks: Checks{
		Unused:            LevelWarning,
		NeverSent:         LevelWarning,
		UnreachableStates: LevelWarning,
		UnreachableCode:   LevelWarning,
		Shadowing:         LevelWarning,
		Order:             LevelWarning,
	}, Naming: Naming{
		Types:         TitleCase,
		EnumConstants: ScreamingCase,
		Events:        TitleCase,
		Machines:      TitleCase,
		States:        TitleCase,
		Modules:       TitleCase,
		Functions:     SnakeCase,
		Variables:     SnakeCase,
		TestCases:     SnakeCase,
		Prefixes:      []string{"t", "e", "tc"},
	}}
}

// Load reads the configuration file at path. What it leaves out keeps its
// default.
func Load(path string) (Config, error) {
	c := Default()
	meta, err := toml.DecodeFile(path, &c)
	if err != nil {
		return Default(), err
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return Default(), fmt.Errorf("%s: unknown setting %s", path, undecoded[0])
	}
	n := c.Naming
	for _, kind := range []Case{n.Types, n.EnumConstants, n.Events, n.Machines, n.States, n.Modules, n.Functions, n.Variables, n.TestCases} {
		if _, ok := patterns[kind]; !ok && kind != Off {
			return Default(), fmt.Errorf("%s: unknown case %q; use TitleCase, camelCase, snake_case, SCREAMING_CASE or off", path, kind)
		}
	}
	k := c.Checks
	for _, level := range []Level{k.Unused, k.NeverSent, k.UnreachableStates, k.UnreachableCode, k.Shadowing, k.Order} {
		if level != LevelWarning && level != LevelError && level != LevelOff {
			return Default(), fmt.Errorf("%s: unknown level %q; use warning, error or off", path, level)
		}
	}
	return c, nil
}

// Find returns the path of the configuration file that applies to files in
// dir, or "" if there's none.
func Find(dir string) string {
	for {
		path := filepath.Join(dir, FileName)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// Conforms reports whether name is written in case c, or is a Hungarian
// prefix from prefixes followed by a TitleCase name. Leading and trailing
// underscores, which P allows, don't count.
func (c Case) Conforms(name string, prefixes []string) bool {
	pattern, ok := patterns[c]
	if !ok {
		return true
	}
	name = strings.Trim(name, "_")
	if name == "" || pattern.MatchString(name) {
		return true
	}
	for _, p := range prefixes {
		if rest, ok := strings.CutPrefix(name, p); ok && p != "" && patterns[TitleCase].MatchString(rest) {
			return true
		}
	}
	return false
}

// Suggest returns name rewritten to conform to case c: if it starts with
// one of prefixes and a capital, as the prefix and the rest in TitleCase,
// otherwise all of it in case c. It returns "" if it can't.
func (c Case) Suggest(name string, prefixes []string) string {
	core := strings.Trim(name, "_")
	for _, p := range prefixes {
		rest, ok := strings.CutPrefix(core, p)
		if !ok || p == "" || rest == "" || !unicode.IsUpper([]rune(rest)[0]) {
			continue
		}
		if fixed := p + TitleCase.Convert(rest); c.Conforms(fixed, prefixes) {
			return strings.Replace(name, core, fixed, 1)
		}
	}
	if fixed := c.Convert(name); fixed != "" && c.Conforms(fixed, nil) {
		return fixed
	}
	return ""
}

// Convert rewrites name in case c, keeping any leading and trailing
// underscores, or returns "" if it can't.
func (c Case) Convert(name string) string {
	core := strings.Trim(name, "_")
	words := Words(core)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	for i, w := range words {
		switch c {
		case TitleCase:
			b.WriteString(capitalize(w))
		case CamelCase:
			if i == 0 {
				b.WriteString(strings.ToLower(w))
			} else {
				b.WriteString(capitalize(w))
			}
		case SnakeCase, ScreamingCase:
			if i > 0 {
				b.WriteByte('_')
			}
			if c == SnakeCase {
				b.WriteString(strings.ToLower(w))
			} else {
				b.WriteString(strings.ToUpper(w))
			}
		default:
			return ""
		}
	}
	start := strings.Index(name, core)
	return name[:start] + b.String() + name[start+len(core):]
}

// Words splits a name into its words, at underscores and changes of case:
// requestCount, request_count, RequestCount and REQUEST_COUNT are all
// request and count. A run of capitals is one word, as in HTTPServer.
// Digits belong to the word before them.
func Words(name string) []string {
	var words []string
	runes := []rune(name)
	start := 0
	for i, r := range runes {
		switch {
		case r == '_':
			if i > start {
				words = append(words, string(runes[start:i]))
			}
			start = i + 1
		case i > start && unicode.IsUpper(r):
			prev := runes[i-1]
			// lowerUpper, or the last capital of a run before a lower case
			// letter: HTTPServer is HTTP Server.
			if unicode.IsLower(prev) || unicode.IsDigit(prev) ||
				(unicode.IsUpper(prev) && i+1 < len(runes) && unicode.IsLower(runes[i+1])) {
				words = append(words, string(runes[start:i]))
				start = i
			}
		}
	}
	if start < len(runes) {
		words = append(words, string(runes[start:]))
	}
	return words
}

func capitalize(w string) string {
	r := []rune(strings.ToLower(w))
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
