// Package doc writes reference documentation for P projects as AsciiDoc,
// organized like Go's package documentation: a page for each project, with
// an overview, an index of everything it declares, and a section for each
// declaration, and an index page listing the projects.
//
// Events handled by exactly one machine are documented under it, as what
// it accepts, the way godoc lists a type's constructors under the type.
package doc

import (
	"cmp"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bencornia/p-lsp/internal/analysis"
)

// Project is a project to document.
type Project struct {
	Name string
	// Dir is the directory of its .pproj file.
	Dir string
	// Files are its own source files, which Package documents.
	Files []string
	// Includes are the projects it includes, directly or through others,
	// by their .pproj paths.
	Includes []string
	// Path is its .pproj file.
	Path    string
	Package analysis.Package
	// Overview is the AsciiDoc the page opens with, such as a README.
	Overview string
}

// Site is a set of projects documented together, whose pages link to each
// other.
type Site struct {
	// Title is the index page's title.
	Title    string
	Projects []*Project
	// Private documents machines' variables and functions too.
	Private bool

	pages   map[*Project]string
	anchors map[*Project]map[string]bool
	byPath  map[string]*Project
}

// NewSite makes a site of projects, giving each a page named for it.
func NewSite(title string, projects []*Project, private bool) *Site {
	s := &Site{
		Title: title, Projects: projects, Private: private,
		pages: map[*Project]string{}, anchors: map[*Project]map[string]bool{}, byPath: map[string]*Project{},
	}
	taken := map[string]bool{"index.adoc": true}
	for _, p := range projects {
		name := p.Name + ".adoc"
		for i := 2; taken[strings.ToLower(name)]; i++ {
			name = fmt.Sprintf("%s-%d.adoc", p.Name, i)
		}
		taken[strings.ToLower(name)] = true
		s.pages[p] = name
		s.byPath[p.Path] = p
		s.anchors[p] = (&page{site: s, project: p}).anchors()
	}
	return s
}

// Page returns the name of p's page.
func (s *Site) Page(p *Project) string {
	return s.pages[p]
}

// WritePage writes p's page.
func (s *Site) WritePage(w io.Writer, p *Project) error {
	pg := &page{site: s, project: p}
	pg.write()
	_, err := io.WriteString(w, pg.String())
	return err
}

// WriteIndex writes the index page, which lists the projects.
func (s *Site) WriteIndex(w io.Writer) error {
	var b strings.Builder
	fmt.Fprintf(&b, "= %s\n\n", s.Title)
	projects := slices.Clone(s.Projects)
	slices.SortStableFunc(projects, func(a, b *Project) int { return compareNames(a.Name, b.Name) })
	root := commonDir(projects)
	b.WriteString("[cols=\"2,2,3\"]\n|===\n|Project |Directory |Synopsis\n\n")
	for _, p := range projects {
		dir, err := filepath.Rel(root, p.Dir)
		if err != nil {
			dir = p.Dir
		}
		fmt.Fprintf(&b, "|xref:%s[%s]\n|%s\n|%s\n\n", s.pages[p], p.Name, code(filepath.ToSlash(dir)), Synopsis(p.Overview))
	}
	b.WriteString("|===\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// commonDir returns the deepest directory holding every project's.
func commonDir(projects []*Project) string {
	if len(projects) == 0 {
		return ""
	}
	root := filepath.Dir(projects[0].Dir)
	for _, p := range projects {
		for root != filepath.Dir(root) {
			if rel, err := filepath.Rel(root, p.Dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				break
			}
			root = filepath.Dir(root)
		}
	}
	return root
}

// Synopsis returns the first sentence of overview's first paragraph,
// skipping titles, attributes, comments and block markup.
func Synopsis(overview string) string {
	var para []string
	for _, line := range strings.Split(overview, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			if len(para) > 0 {
				return firstSentence(strings.Join(para, " "))
			}
		case strings.HasPrefix(line, "="), strings.HasPrefix(line, ":"), strings.HasPrefix(line, "//"),
			strings.HasPrefix(line, "["), strings.HasPrefix(line, "."):
		default:
			para = append(para, line)
		}
	}
	return firstSentence(strings.Join(para, " "))
}

func firstSentence(text string) string {
	if i := strings.Index(text, ". "); i >= 0 {
		return text[:i+1]
	}
	return text
}

// page renders one project's page.
type page struct {
	site    *Site
	project *Project
	strings.Builder
	// accepts maps each machine to the events documented under it.
	accepts map[string][]analysis.Event
	// topEvents are the events documented at the top level.
	topEvents []analysis.Event
}

func (pg *page) group() {
	if pg.accepts != nil {
		return
	}
	pg.accepts = map[string][]analysis.Event{}
	declared := map[string]bool{}
	for _, m := range pg.project.Package.Machines {
		if m.Kind == "machine" {
			declared[m.Name] = true
		}
	}
	for _, e := range sorted(pg.project.Package.Events, func(e analysis.Event) string { return e.Name }) {
		var handlers []string
		for _, u := range e.Uses {
			if (u.Verb == "on" || u.Verb == "receive") && u.MachineKind == "machine" && !slices.Contains(handlers, u.Machine) {
				handlers = append(handlers, u.Machine)
			}
		}
		if len(handlers) == 1 && declared[handlers[0]] {
			pg.accepts[handlers[0]] = append(pg.accepts[handlers[0]], e)
		} else {
			pg.topEvents = append(pg.topEvents, e)
		}
	}
}

// anchors returns the anchors the page defines.
func (pg *page) anchors() map[string]bool {
	a := map[string]bool{}
	pkg := pg.project.Package
	for _, d := range pkg.Types {
		a[anchor("type", d.Name)] = true
	}
	for _, e := range pkg.Events {
		a[anchor("event", e.Name)] = true
	}
	for _, d := range pkg.EventSets {
		a[anchor("eventset", d.Name)] = true
	}
	for _, d := range pkg.Interfaces {
		a[anchor("interface", d.Name)] = true
	}
	for _, m := range pkg.Machines {
		a[anchor("machine", m.Name)] = true
		for _, s := range m.States {
			a[stateAnchor(m.Name, s.Name)] = true
		}
		if pg.site.Private {
			for _, f := range m.Functions {
				a[memberAnchor(m.Name, "function", f.Name)] = true
			}
			for _, v := range m.Variables {
				a[memberAnchor(m.Name, "var", v.Name)] = true
			}
		}
	}
	for _, d := range pkg.Functions {
		a[anchor("function", d.Name)] = true
	}
	for _, d := range pkg.Params {
		a[anchor("param", d.Name)] = true
	}
	for _, d := range pkg.Modules {
		a[anchor("module", d.Name)] = true
	}
	for _, d := range pkg.Tests {
		a[anchor("test", d.Name)] = true
	}
	return a
}

func anchor(kind, name string) string { return kind + "-" + name }

func stateAnchor(machine, state string) string { return "machine-" + machine + "-state-" + state }

func memberAnchor(machine, kind, name string) string {
	return "machine-" + machine + "-" + kind + "-" + name
}

// link links to the anchor on this page, or on the page of a project this
// one includes, or else is just text.
func (pg *page) link(anchor, text string) string {
	if pg.site.anchors[pg.project][anchor] {
		return fmt.Sprintf("<<%s,%s>>", anchor, text)
	}
	for _, path := range pg.project.Includes {
		if p := pg.site.byPath[path]; p != nil && pg.site.anchors[p][anchor] {
			return fmt.Sprintf("xref:%s#%s[%s]", pg.site.pages[p], anchor, text)
		}
	}
	return text
}

// code formats text as literal monospace.
func code(text string) string {
	return "`+" + text + "+`"
}

func (pg *page) linkCode(anchor, name string) string {
	return pg.link(anchor, code(name))
}

func (pg *page) write() {
	pg.group()
	p := pg.project
	pkg := p.Package
	fmt.Fprintf(pg, "= %s\n\n", p.Name)
	if overview := strings.TrimSpace(p.Overview); overview != "" {
		pg.WriteString(overview + "\n\n")
	}
	var includes []string
	for _, path := range p.Includes {
		if inc := pg.site.byPath[path]; inc != nil {
			includes = append(includes, fmt.Sprintf("xref:%s[%s]", pg.site.pages[inc], inc.Name))
		} else {
			includes = append(includes, code(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))))
		}
	}
	if len(includes) > 0 {
		fmt.Fprintf(pg, "Includes: %s\n\n", strings.Join(includes, ", "))
	}

	machines, specs := splitMachines(pkg.Machines)
	pg.index(machines, specs)

	pg.docs("types", "Types", "type", pkg.Types)
	if len(pg.topEvents) > 0 {
		pg.WriteString("[[events]]\n== Events\n\n")
		for _, e := range pg.topEvents {
			pg.event(e, 2)
		}
	}
	pg.docs("eventsets", "Event sets", "eventset", pkg.EventSets)
	pg.docs("interfaces", "Interfaces", "interface", pkg.Interfaces)
	if len(machines) > 0 {
		pg.WriteString("[[machines]]\n== Machines\n\n")
		for _, m := range machines {
			pg.machine(m)
		}
	}
	if len(specs) > 0 {
		pg.WriteString("[[specs]]\n== Specs\n\n")
		for _, m := range specs {
			pg.machine(m)
		}
	}
	pg.docs("functions", "Functions", "function", pkg.Functions)
	pg.docs("params", "Parameters", "param", pkg.Params)
	pg.docs("modules", "Modules", "module", pkg.Modules)
	pg.docs("tests", "Tests", "test", pkg.Tests)

	if len(p.Files) > 0 {
		pg.WriteString("[[files]]\n== Files\n\n")
		for _, f := range p.Files {
			rel, err := filepath.Rel(p.Dir, f)
			if err != nil {
				rel = f
			}
			fmt.Fprintf(pg, "* %s\n", code(filepath.ToSlash(rel)))
		}
		pg.WriteString("\n")
	}
}

func splitMachines(all []analysis.Machine) (machines, specs []analysis.Machine) {
	for _, m := range sorted(all, func(m analysis.Machine) string { return m.Name }) {
		if m.Kind == "machine" {
			machines = append(machines, m)
		} else {
			specs = append(specs, m)
		}
	}
	return machines, specs
}

// index writes the page's index: every declaration, linked, by kind.
func (pg *page) index(machines, specs []analysis.Machine) {
	pkg := pg.project.Package
	var b strings.Builder
	list := func(id, title, kind string, docs []analysis.Doc) {
		if len(docs) == 0 {
			return
		}
		fmt.Fprintf(&b, "* <<%s,%s>>\n", id, title)
		for _, d := range sorted(docs, docName) {
			fmt.Fprintf(&b, "** %s\n", pg.linkCode(anchor(kind, d.Name), d.Name))
		}
	}
	machineList := func(id, title string, ms []analysis.Machine) {
		if len(ms) == 0 {
			return
		}
		fmt.Fprintf(&b, "* <<%s,%s>>\n", id, title)
		for _, m := range ms {
			fmt.Fprintf(&b, "** %s\n", pg.linkCode(anchor("machine", m.Name), m.Name))
			if accepts := pg.accepts[m.Name]; len(accepts) > 0 {
				var links []string
				for _, e := range accepts {
					links = append(links, pg.linkCode(anchor("event", e.Name), e.Name))
				}
				fmt.Fprintf(&b, "*** accepts %s\n", strings.Join(links, ", "))
			}
		}
	}

	list("types", "Types", "type", pkg.Types)
	var events []analysis.Doc
	for _, e := range pg.topEvents {
		events = append(events, e.Doc)
	}
	list("events", "Events", "event", events)
	list("eventsets", "Event sets", "eventset", pkg.EventSets)
	list("interfaces", "Interfaces", "interface", pkg.Interfaces)
	machineList("machines", "Machines", machines)
	machineList("specs", "Specs", specs)
	list("functions", "Functions", "function", pkg.Functions)
	list("params", "Parameters", "param", pkg.Params)
	list("modules", "Modules", "module", pkg.Modules)
	list("tests", "Tests", "test", pkg.Tests)
	if len(pg.project.Files) > 0 {
		b.WriteString("* <<files,Files>>\n")
	}
	if b.Len() > 0 {
		pg.WriteString("[[index]]\n== Index\n\n" + b.String() + "\n")
	}
}

// docs writes a section of declarations of one kind.
func (pg *page) docs(id, title, kind string, docs []analysis.Doc) {
	if len(docs) == 0 {
		return
	}
	fmt.Fprintf(pg, "[[%s]]\n== %s\n\n", id, title)
	for _, d := range sorted(docs, docName) {
		pg.decl(anchor(kind, d.Name), d, 2)
	}
}

// decl writes a declaration's heading, source and comment, at a section
// level, where 1 is the page's top sections.
func (pg *page) decl(id string, d analysis.Doc, level int) {
	fmt.Fprintf(pg, "[[%s]]\n%s %s\n\n", id, strings.Repeat("=", level+1), code(d.Name))
	pg.source(d.Source)
	pg.comment(d.Comment)
}

func (pg *page) source(src string) {
	fmt.Fprintf(pg, "[source,p]\n----\n%s\n----\n\n", src)
}

func (pg *page) comment(comment string) {
	if comment != "" {
		pg.WriteString(comment + "\n\n")
	}
}

// event writes an event and where it's used.
func (pg *page) event(e analysis.Event, level int) {
	pg.decl(anchor("event", e.Name), e.Doc, level)
	labels := []struct{ label, verb string }{
		{"Sent by", "send"}, {"Raised by", "raise"}, {"Announced by", "announce"},
		{"Handled by", "handle"}, {"Deferred by", "defer"}, {"Ignored by", "ignore"}, {"Observed by", "observe"},
	}
	var lines []string
	for _, l := range labels {
		var places []string
		for _, u := range e.Uses {
			verb := u.Verb
			if verb == "on" || verb == "receive" {
				if u.MachineKind != "machine" {
					// A spec's handlers are what it observes.
					continue
				}
				verb = "handle"
			}
			if verb != l.verb {
				continue
			}
			if place := pg.place(u); !slices.Contains(places, place) {
				places = append(places, place)
			}
		}
		if len(places) > 0 {
			lines = append(lines, fmt.Sprintf("%s:: %s", l.label, strings.Join(places, ", ")))
		}
	}
	if len(lines) > 0 {
		pg.WriteString("[horizontal]\n" + strings.Join(lines, "\n") + "\n\n")
	}
}

// place describes where an event is used.
func (pg *page) place(u analysis.EventUse) string {
	if u.Machine == "" {
		return pg.linkCode(anchor("function", u.Function), u.Function)
	}
	place := pg.linkCode(anchor("machine", u.Machine), u.Machine)
	switch {
	case u.Verb == "observe":
	case u.State != "":
		place += " in " + pg.linkCode(stateAnchor(u.Machine, u.State), u.State)
	case u.Function != "":
		place += " in " + pg.linkCode(memberAnchor(u.Machine, "function", u.Function), u.Function)
	}
	return place
}

// machine writes a machine or spec: what it accepts, its states, and its
// variables and functions if the site is private.
func (pg *page) machine(m analysis.Machine) {
	pg.decl(anchor("machine", m.Name), m.Doc, 2)
	if len(m.Observes) > 0 {
		var links []string
		for _, e := range m.Observes {
			links = append(links, pg.linkCode(anchor("event", e), e))
		}
		fmt.Fprintf(pg, "Observes: %s\n\n", strings.Join(links, ", "))
	}
	if accepts := pg.accepts[m.Name]; len(accepts) > 0 {
		pg.WriteString("==== Accepts\n\n")
		for _, e := range accepts {
			pg.event(e, 4)
		}
	}
	if len(m.States) > 0 {
		pg.WriteString("==== States\n\n")
		for _, s := range m.States {
			pg.state(m.Name, s)
		}
	}
	if pg.site.Private {
		pg.members(m.Name, "Variables", "var", m.Variables)
		pg.members(m.Name, "Functions", "function", m.Functions)
	}
}

func (pg *page) members(machine, title, kind string, docs []analysis.Doc) {
	if len(docs) == 0 {
		return
	}
	fmt.Fprintf(pg, "==== %s\n\n", title)
	for _, d := range docs {
		pg.decl(memberAnchor(machine, kind, d.Name), d, 4)
	}
}

// state writes a state: what kind it is, its comment, and what it does,
// as P would say it.
func (pg *page) state(machine string, s analysis.State) {
	fmt.Fprintf(pg, "[[%s]]\n===== %s\n\n", stateAnchor(machine, s.Name), code(s.Name))
	var kind []string
	if s.Start {
		kind = append(kind, "start state")
	}
	if s.Temperature != "" {
		kind = append(kind, s.Temperature)
	}
	if len(kind) > 0 {
		k := strings.Join(kind, ", ")
		fmt.Fprintf(pg, "_%s%s._\n\n", strings.ToUpper(k[:1]), k[1:])
	}
	pg.comment(s.Comment)
	if len(s.Items) == 0 {
		return
	}
	for _, it := range s.Items {
		pg.WriteString("* " + pg.item(machine, it) + "\n")
	}
	pg.WriteString("\n")
}

func (pg *page) item(machine string, it analysis.StateItem) string {
	block := code("{ … }")
	fn := func() string { return pg.linkCode(memberAnchor(machine, "function", it.Function), it.Function) }
	var events []string
	for _, e := range it.Events {
		events = append(events, pg.linkCode(anchor("event", e), e))
	}
	switch it.Kind {
	case "entry", "exit":
		if it.Function != "" {
			return "*" + it.Kind + "* " + fn()
		}
		return "*" + it.Kind + "* " + block
	case "defer", "ignore":
		return "*" + it.Kind + "* " + strings.Join(events, ", ")
	}
	text := "*on* " + strings.Join(events, ", ")
	switch {
	case it.Goto != "" && it.Function != "":
		text += " *goto* " + pg.linkCode(stateAnchor(machine, it.Goto), it.Goto) + " *with* " + fn()
	case it.Goto != "":
		text += " *goto* " + pg.linkCode(stateAnchor(machine, it.Goto), it.Goto)
	case it.Function != "":
		text += " *do* " + fn()
	default:
		text += " *do* " + block
	}
	return text
}

func docName(d analysis.Doc) string { return d.Name }

// sorted returns a copy of items sorted by name, ignoring case, as godoc
// sorts its index.
func sorted[T any](items []T, name func(T) string) []T {
	out := slices.Clone(items)
	slices.SortStableFunc(out, func(a, b T) int { return compareNames(name(a), name(b)) })
	return out
}

func compareNames(a, b string) int {
	return cmp.Or(cmp.Compare(strings.ToLower(a), strings.ToLower(b)), cmp.Compare(a, b))
}
