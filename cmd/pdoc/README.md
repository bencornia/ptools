# pdoc

pdoc writes reference documentation for P projects as AsciiDoc,
organized like Go's package documentation. Each project (`.pproj`) gets a
page:

- **Overview:** the `README.adoc` beside the `.pproj`, if there is one,
  without its title.
- **Index:** everything the project declares, linked, by kind: types,
  events, event sets, machines, specs, functions, parameters, modules and
  tests, sorted by name.
- **A section for each declaration:** its source, without bodies, and its
  doc comment, the comment directly above it, passed through as AsciiDoc.
- **Event flow:** each event lists where it's sent, raised, announced,
  handled, deferred, ignored and observed. An event handled by exactly one
  machine is documented under that machine, as one it accepts, the way
  godoc lists a type's constructors under the type.
- **States:** in source order, with what each does: `entry`, `exit`, `on …
  goto`/`do`, `defer` and `ignore`, linked to the events and states they
  name.
- **Files:** the project's source files.

With `-o dir`, it writes a page for each project found under the paths
given (the current directory by default) and an `index.adoc` listing them,
with each one's directory and the first sentence of its overview. Pages
link to the pages of the projects they include. Without `-o`, it prints the
page of the one project found. `--private` documents machines' variables and
functions too, and `--title` sets the index page's title. Asciidoctor turns
the pages into HTML:

```sh
pdoc -o docs .
asciidoctor docs/*.adoc
```

Install it as the [top-level README](../../README.md) says.

Doc comments are the comment directly above a declaration, with no blank
line between. A comment after code on the line above isn't one, and lines
of stars drawing a banner are dropped.
