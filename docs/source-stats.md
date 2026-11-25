# Source statistics

`sourcestats` counts the lines of code in a source tree and says how many of
them a person wrote. It is the one implementation of
`codefly/source-stats/v1`: the figure a release carries so that whoever
installs it can see its size without having its source.

## What core does, and what it does not do yet

Core counts a tree and validates a count. **Nothing calls it yet**: no
command counts a unit when it is packaged, no manifest carries the figure and
no host shows it. Those belong to the CLI, the host's manifest library and the
host.

## The figure

```yaml
schema: codefly/source-stats/v1
languages:
  - language: go
    handwritten: 103
    generated: 7
    tests: 133
```

One number would mislead: a unit built on generated clients is mostly code
nobody wrote. So every language is counted three ways, and `handwritten` is
the one that says how much there is to read and maintain.
`Stats.Handwritten()` sums it over every language, for a reader that shows a
single figure.

Languages are listed once each, sorted by name, and a language with nothing
counted is left out. `Stats.Validate` holds a figure that arrived in a release
to exactly that before anything is shown.

## What is counted

A line of code is a line that is neither blank nor only a comment, in a file
whose extension names a programming language (`sourcestats/languages.go`).
YAML, JSON, Markdown and lockfiles are configuration, data and prose, and are
not counted.

A file is one of three things, decided in this order:

| A file is | When |
| --- | --- |
| generated | Its name follows a generator's convention (`*.pb.go`, `*_gen.go`, `*_pb2.py`, `*_pb.ts`, a `generated/` directory); or one of its first five non-blank lines carries a generated-code marker; or the caller says so. |
| tests | Its name or a directory above it follows a test convention (`*_test.go`, `test_*.py`, `*.test.tsx`, `tests/`, `__tests__/`, `testdata/`). |
| handwritten | Otherwise. |

A bare "do not edit" is not a marker: people write that about files they
maintain by hand.

`.git`, `node_modules`, `vendor`, `__pycache__` and `.venv` are never
counted. Build output (`dist`, `build`, `target`) is **not** skipped by name,
because those are also ordinary package names. The caller knows what the
repository tracks and ignores, and says so:

```go
stats, err := sourcestats.Count(os.DirFS(dir), sourcestats.Options{
    Skip:      func(name string, dir bool) bool { return ignored(name) },
    Generated: func(name string) bool { return linguistGenerated(name) },
})
```

## What the count is not

It is a size, not a measure of quality or effort. It is approximate by
design: comments are recognised by how a line begins, without parsing the
language. So a Python docstring reads as code, and a block comment opened
after code on the same line is not followed — `/*` is far more often part of
a glob in a string (`"**/*.go"`) than the start of a trailing comment.

The same tree always counts the same, which is what a figure bound to a
release needs.
