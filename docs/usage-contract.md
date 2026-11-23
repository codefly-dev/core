# The usage contract

The usage contract is what an owner tells the code that consumes it. A
service agent or a module publishes it as `usage.codefly.yaml` beside its
manifest, and it travels with the owner's release, so a consumer reads the
rules of the version it pinned.

It is not a repository's `AGENTS.md`. That file is for whoever changes the
repository. This one is for whoever calls it: the service that depends on a
database agent, the solution that consumes a module.

**`contracts/usage`** — Go package `usage`, imported as `usagecontract` — is
the one implementation of `codefly/usage/v1`: the model, a strict decoder,
35 named rules and the fixtures every reader is driven through. Nothing
outside the package parses or validates a usage contract.

## What core does, and what it does not do yet

Core reads and validates a contract, carries an agent's contract to whoever
asks the agent about itself, and matches a contract's detect clauses against a
semantic index. Two things the design builds on that are
**not built anywhere yet**, and nothing below should be read as saying they
are:

- Nothing resolves which contracts apply to a service from its dependencies.
- No command reports a finding. `Detect` (below) matches a contract against a
  semantic index; nothing yet fetches the index from a service's agent and
  prints what it found.

## How an agent serves one

An agent embeds its `usage.codefly.yaml` beside `agent.codefly.yaml` and sets
`services.Advertisement.UsageContract` to the file's text. It reaches a host
in `AgentInformation.usage_contract`, verbatim: core neither parses nor
rewrites it on the way, so the file has one reader. An empty field means the
agent states no usage rules, which is also what an agent built before this
field says; the two need no telling apart, because either way there are no
rules. That is why the field is no capability of the agent contract
([agent-contract.md](agent-contract.md)) and `agents/contract/contract.json`
is unchanged.

An agent holds its own file to the rules in its own suite:

```go
if _, err := usagecontract.Parse(usageDocument); err != nil { t.Fatal(err) }
```

## The file

```yaml
schema: codefly/usage/v1
summary: Reach the store only through its library, which owns roles and tenant isolation.
rules:
  - id: STORE-CONN-001
    title: Open the store only through the library
    severity: error
    since: 1.4.0
    rationale: A raw connection bypasses the non-privileged role and the tenant checks.
    do: Request a scoped reader or writer from the library and run inside its transaction.
    dont: Open a connection or a pool yourself.
    docs: docs/usage.md
    detect:
      - language: go
        forbid-call: [database/sql.Open]
        forbid-import: [github.com/example/rawdriver]
      - language: python
        forbid-call: [psycopg.connect]
    except-paths: ["**/migrationtest/**"]
  - id: STORE-MAINT-001
    title: Keep maintenance out of request handlers
    severity: warn
    since: 1.5.0
    rationale: Maintenance clears the tenant, so a handler holding it serves one tenant with another's reach.
    do: Hand request handlers the authenticated factory only.
```

| Field | Meaning |
| --- | --- |
| `summary` | One paragraph: what consuming this owner correctly comes down to. |
| `rules[].id` | The rule's name for the life of the owner. An exception names it and a finding reports it, so it is never renumbered and never reused. |
| `rules[].severity` | `error` or `warn`. An owner introduces a rule as `warn` and may raise it in a later release, so pinning a newer owner never fails a consumer that had no release in which to hear about the rule. |
| `rules[].since` | The owner's release that introduced the rule. |
| `rules[].rationale`, `do`, `dont` | What a coding agent or a person is told. `dont` is optional. |
| `rules[].docs` | A path inside the owner's release. Optional. |
| `rules[].detect` | How a linter recognises a violation, one clause per language. Optional: a rule without one takes judgement. |
| `rules[].except-paths` | Globs, relative to the consuming service, where detection does not apply. |

## One rule, two strengths

A rule's text is what a reader is told. Its `detect` clauses are what a
linter checks. Both come from one entry, so the prose and the check cannot
say different things.

Detection is a closed vocabulary, so reading a contract runs nothing:

| Clause field | Forbids |
| --- | --- |
| `forbid-call` | Calling a function or method, named literally and qualified by what owns it: `database/sql.Open`, `psycopg.connect`. |
| `forbid-import` | Importing a package or module, named literally. |

A bare name (`Open`) is refused because it would match every function so
named in a consumer. A pattern (`database/sql.*`) is refused because v1 has
none.

`language` is a name and not an enumeration. A language a reader has no
linter for is a clause that reader skips, never a malformed contract, so a
new language needs no release of core.

Known limits of v1: a method on a receiver type (Go's `(*pgx.Conn).Exec`) and
a `::`-qualified path cannot be spelled, and there is no clause that requires
a call rather than forbidding one.

## Detecting violations

```go
findings := contract.Detect(index) // index: the consumer's basev0.SemanticIndex
```

`Detect` reads a consumer's source through its semantic index — the body-free
projection a language agent already produces (`Code.GetSemanticIndex`) — so it
parses no language and reads no file. A `Finding` names the rule, the
forbidden symbol, the file and, for a call, the line.

It reports what it can be sure of and nothing else:

| Clause | Matches when |
| --- | --- |
| `forbid-import` | A file imports the package or module, or anything beneath it (`github.com/lib/pq/oid`, `psycopg.rows`, `sqlx::postgres`). |
| `forbid-call` | The file imports what owns the function **and** calls it the way that import is ordinarily named: `pgxpool.New`, `pgx.Connect` for `…/pgx/v5`, `psycopg.connect`. |

The index records calls as written and does not say what a name is bound to.
So two things are **missed, never guessed**: a call through an import alias
(`import pg "…/pgxpool"`, `import psycopg as pg`), and a call of a name
imported on its own (`from psycopg import connect`, `import { connect }`). A
finding that might be the consumer's own function would teach people to stop
reading findings. An owner that needs a module kept out entirely says so with
`forbid-import`, which has neither gap. Closing the gap properly means the
index carrying import bindings.

A file matching one of the rule's `except-paths` is skipped for that rule.
In a glob, `**` crosses directories and `*` and `?` do not; nothing else is
special.

## Why it is bounded

A contract's text reaches a model's context, and it comes from a dependency.
So the schema, not each reader, bounds the file, every list and every text
field, and refuses invisible characters. `docs` is a path and never a URL, and
there is no field that holds a command.

## Reading one

```go
contract, err := usagecontract.Load(dir)   // os.ErrNotExist: the owner says nothing
contract, err := usagecontract.Parse(data)
clause := contract.Rule("STORE-CONN-001").Detects("go") // nil: no linter checks go
```

`Validate` holds a contract built in memory to the same rules, and `Encode`
refuses to write one `Parse` would refuse.

## The rules

Each refusal is one named rule, applied in this order, so a document is
refused for one reason whichever reader refused it.

| Rule | Holds |
| --- | --- |
| `well-formed` | The file is a YAML document. |
| `document-bounded` | The file is at most 256 KiB. |
| `schema` | `schema` is `codefly/usage/v1`. Another schema is refused as a version skew (`ErrSchema`), not as malformed. |
| `mapping-keys-once` | No mapping names a key twice, aliases included. |
| `no-merge-keys` | No mapping uses the merge key `<<`. |
| `no-nulls` | No explicit null: write a field or leave it out. |
| `whole-numbers` | No fractional number; a version such as `since` is text. |
| `key-is-a-name` | Every mapping key is a non-empty name. |
| `known-fields` | No field the schema does not declare. This is what refuses a contract carrying a command. |
| `one-document` | The file holds one document. |
| `summary` | `summary` is present. |
| `rules-declared` | At least one rule. An owner with nothing to say publishes no contract. |
| `rules-bounded` | At most 64 rules. |
| `rule-id-shape` | An `id` is upper-case parts joined by hyphens: `PG-CONN-001`. |
| `rule-ids-unique` | An `id` names one rule. |
| `rule-title` | A one-line `title`. |
| `rule-severity` | `severity` is `error` or `warn`. |
| `rule-since` | `since` is a semantic version without a leading `v`. |
| `rule-rationale` | `rationale` is present. |
| `rule-do` | `do` is present: a rule that only forbids leaves its reader to guess the alternative. |
| `rule-text-bounded` | `summary` is at most 512 characters, a `title` 120, and each of `rationale`, `do` and `dont` 2048. |
| `rule-text-printable` | Text is valid UTF-8 with no control or invisible formatting character other than a line break or a tab. |
| `rule-docs-relative` | `docs` is a clean path inside the owner's release: no URL, no absolute path, no `..`. |
| `detect-bounded` | At most 16 detect clauses in a rule. |
| `detect-language` | A clause's `language` is a lower-case name. |
| `detect-one-per-language` | One clause per language in a rule. |
| `detect-forbids-something` | A clause lists `forbid-call` or `forbid-import`. |
| `detect-symbols-bounded` | At most 64 symbols in a list. |
| `detect-call-shape` | A forbidden call is a literal, qualified name; never a bare name or a pattern. |
| `detect-import-shape` | A forbidden import is a literal package or module name; never a pattern. |
| `detect-symbols-unique` | No symbol twice in a list. |
| `except-paths-need-detect` | `except-paths` only beside `detect`. |
| `except-paths-bounded` | At most 32 excepted paths. |
| `except-paths-relative` | An excepted path is a glob relative to the consuming service that stays inside it. |
| `except-paths-unique` | No excepted path twice. |

## The kit

`usagecontract.Fixtures()` (41 documents) is every accepted and refused
document with the verdict a reader must reach, and `usagecontract.Run` drives
a reader's own entrypoint through them. The package's self-check deletes each
rule in turn and proves a fixture notices; a rule that could be dropped
silently is a rule the kit does not protect.
