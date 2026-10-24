# The module contract

The module contract is what a module asks for. The CLI resolves it into the
`authority` block of the module's signed manifest
([module-manifest.md](module-manifest.md)); a host grants the intersection of
that request with the administrator's approval, never more. It lives in this
repository with one implementation and a shipped kit because three
repositories read it (the CLI's renderer and publisher, the runtimes that
publish contracts, the platform's loader), and a second implementation in any
of them is where the next disagreement about what a file means appears.

The **cell** (`codefly/cell/v1`, the inventory of an environment's cell that a
publish writes and the platform derives its mesh policy, admission set and
RBAC from) has **no implementation in core on `main`**. #701's branch carried
one beside the module contract; the merge did not, and no open PR brings it.
Nothing in this document describes a cell package, and the consumers that read
cells still own their own cell models until core ships one.

**`contracts/module`** — Go package `module`, imported as `modulecontract` by
its consumers and in the examples below, so the identifier says what it is —
is `codefly/module-contract/v1`, the
request a module publishes beside its manifest as
`module.contract.codefly.yaml`: the principal its credentials are issued to,
the operation bindings it redeems (each with a scope ceiling per operation in
one of two spellings — bare actions qualified by the binding's resource-kind
slot, or `{resource_kind, actions}` entries naming the kind literally, never
mixed, never a mapping), the queues and namespaces it declares, its own scope
ceilings and the destinations it exposes. Audience, resource kind and binding
key are **slots** into the composition's workspace configuration
(`{from: <group>/<key>}`), whose key name carries its meaning, and a literal
where a slot belongs is a schema error. `Parse` decodes strictly (an unknown
field — a tenancy, a build digest, an identity — is refused, never ignored),
`Validate` holds every rule — a ceiling in both spellings included, whether
written or constructed, since `Resolve` would mint scopes from both — and
`Resolve` resolves the slots against the values a composition supplies,
reporting every unresolved, secret-classified and ambiguous slot in one error
under its own sentinel (a secret's value never in the message). Records that
are one key to core (`MODEL_AUDIENCE` and `model-audience`) must AGREE on its
value: agreeing spellings are one value and resolve, and it is the disagreement
that has no answer — see the resolution rules below. A publisher writes through
`Encode`, never by marshaling the model.

**A binding may name the calls it makes instead of a ceiling.** For an
operation, `calls:` lists the audience's methods the binding calls, spelled as
the audience's own method policy names them: a gRPC method
(`/documents.v1.DocumentsService/ResolveEvidence`) or an HTTP route
(`GET /v1/model/profiles`). The operation's ceiling is then what those calls
require, read from the audience's declarations when the contract is resolved:
`Resolve(values, modulecontract.WithCallees(callees))`, where `Callees`
answers, for an audience and a call, the scopes its method policy names. A
module therefore never names another module's resource kinds, and a callee that
changes what a call requires changes every caller's ceiling with it. An
operation takes its ceiling from `scope_ceiling` or from `calls`, never both;
a call is spelled as a method or a route and listed once; and a `Resolve` with
no callees, or a call the audience does not serve or that declares nothing, is
refused under `ErrUnresolvedCall`, naming the binding and the call, never
delivered with an empty ceiling.

**A module may declare the methods it serves from a protocol it does not own.**
A method a module defines carries its policy as an option on its own proto; a
method of a protocol the module only serves (an OpenTelemetry collector's
`Export`) has no option the module can set. `foreign_methods:` lists each such
method by its full gRPC name with what a call must hold, in the module's own
vocabulary: every kind is one of its `scope_ceilings` and every action within
that ceiling, and the list names each method once, each requiring at least one
action. The render delivers them beside the methods its published contracts
declare, so the hop in front of the module decides them like any other.

**A solution's consumption is the same request, declared where a solution
already declares it.** A composed solution is a module, but what it calls is
declared once, in `solution.codefly.yaml`'s `api.consumes`, which also drives its
generated client and its runtime's upstream map. Under schema
`codefly.solution-manifest/v1` an entry may ask for authority at the module it
consumes: `scopes` (`{resource_kind, actions}` in that module's permission
vocabulary), an `audience` slot (`{from: <group>/<key>}`) and an optional
`revision`. `Manifest.ConsumptionContract(principal)` turns the scoped entries
into a **module contract** — one **invoke** binding per entry, its ID the entry's,
its ceiling exactly the declared scopes — so the request is judged by this
package's rules and resolved by its `Resolve`, exactly as a module's published
contract is: the audience a module verifies is the composition's to say and is
not always the name it is composed under, and a solution cannot spell a kind or
an action a contract would refuse. The principal is the name the solution is
composed under. An entry without scopes asks for nothing; `scopes: []` is
refused, not read as absent; a v0 manifest cannot carry a request, so an older
reader refuses the version rather than reading a request as nothing.

`LoadConsumption` reads the schema version and `api.consumes` strictly and
nothing else, for a renderer that derives authority from the consumption alone:
an unknown or invalid field in a consumption fails, while a section unrelated to
it never fails a render that asks for no authority. The runtime projection
(`ConsumedAPI`, `CODEFLY__API_CONSUMES`) carries the declared scopes, so a runtime
can refuse a mint outside the declaration it was rendered from — written once, in
the YAML.

**Resolution is core's too, not a provider's.** A contract's slots resolve
against a composition's workspace configuration, and *which* record answers a
slot is as much a rule of this contract as any refusal of a document: two
consumers resolving one composition into two different authority requests is
the same failure as two readers disagreeing about a file. So `Values` only
ENUMERATES — a provider reports a group's records as supplied, keeping both
spellings of a key, a key supplied twice and a key classified two ways — and
every rule over them lives in `contracts/module`: a key is matched
in either spelling core accepts, records that are one key must agree on its
value (**two spellings that agree are one value and resolve** — it is the
disagreement that has no answer), a key any occurrence of which is secret IS a
secret (so a slot carrying it is refused rather than inlined), a resolved value
is one non-empty line, and
a resolved RESOURCE KIND is held to the grammar a literal one is — without
which a composition supplying `documents.passages:delete,documents.passages`
turns one declared action into two apparent scopes. `RunResolution` drives a
consumer's own provider through shipped configurations — competing spellings
that agree and that disagree, a record supplied twice, a public and a secret
occurrence, a missing key, a kind carrying a comma or a colon — so a provider
that collapses two spellings, drops the secret occurrence or deduplicates fails
by name.

**A writer gets bytes or an error.** `Contract.Encode` is the only way to
write a contract: it validates, marshals, and reads the bytes back through its
own reader, returning them only when what comes back is what went in.
Marshaling the model directly let a publisher emit a document its reader
refuses — a principal of `INVALID PRINCIPAL` — with the failure surfacing at
whoever READ the file, or at admission rather than at publish; and, worse, one
that parses to something else, which no reader refuses at all. Writing through `Encode` rather than marshaling
the model is a REQUIREMENT on each consumer, which the adoption table carries
and which this repository cannot establish for them.

**Every refusal CONDITION is protected by construction**, and the
construction is two properties that hold together rather than one scan.

*A rule holds exactly one condition.* A rule is a function in `rules.go`, and
`TestEveryRuleHoldsExactlyOneCondition` enumerates the refusals in each from
the package's own source: a second `fmt.Errorf` added inside an existing rule
fails at that function, and a refusal written in a function that is no rule's
check fails too, because nothing could be required to protect it.

*Every rule is falsified by a fixture.* `TestEveryRuleIsProtectedByAFixture`
deletes each rule in turn and requires a shipped fixture to notice — and to
notice by its own message, so a refusal arriving from a later rule does not
count. A new rule therefore cannot exist without a witness.

Together they leave no third place to put a condition: inside a rule it is
caught by the first, as a new rule it is caught by the second. That matters
because the earlier mechanism — enumerate the refusal sites, require each to
be *reached* by some fixture — proved a rule was reached and said nothing
about a condition sharing a rule with another. Nine rounds of review on #701
found that difference five times, each a weakened predicate that accepted an
invalid document while every fixture went on refusing, because a sibling
condition in the same rule reached the same refusal site. Each such rule was
split into one rule per condition, each with its own witness.

The reachability scan remains beside them
(`TestEveryRefusalConditionIsReachedByAFixture`), covering the refusals that
live outside the rule table — the parser's and the writer's. A condition no
document can reach is declared with its reason and asserted to be genuinely
unreached, so the one open guard is written down rather than silent.

The enumeration itself is now tested
(`internal/conditions`), because it is what decides whether a
condition is protected and two of its own defects had read as "everything is
protected": the enclosing function was tracked on a stack popped at every
node rather than at every declaration, and a format string written as a
concatenation was not read at all — so a refusal spelled across two lines was
invisible to the check for witnesses. One such refusal existed here and was
found by the rule-to-condition pairing, not by review.

**One strict decoding path.** A type with its own `UnmarshalYAML` does not get
yaml's `KnownFields`, so each custom decoder re-implemented the same
strictness and each was fixed separately, a round apart. Mappings are now read
through `wire.ReadMapping` — key decoded with its type, unknown field
reported, malformed value kept — and a decoder that handles a mapping without
it fails `TestEveryCustomDecoderReadsMappingsThroughTheOnePath`.

**Three refusals are properties of the DOCUMENT, not of a typed decoder**, and
live once in `internal/wire` because one guard per decoder is how a
dynamic map came to have none. One mapping names one key once, with aliases
RESOLVED — yaml compares raw key nodes first, so an alias key silently replaced
a per-operation ceiling. An ALIAS is its target, resolved under a bound: an
anchored fraction declared as a key and used where a whole number belongs
reached an integer field truncated, because keys and alias targets were
examined by neither check. A key now gets every check a value gets, through
the same function. An explicit **null** is refused anywhere: decoding null
into a string returns false rather than an error, so a typed decoder skips the
key *and its value* and omits the sequence element — `null: {tenancy:
dedicated}` discarded a subtree past a boundary advertised as strict. And a
mapping key must be a non-empty name, because the model's maps are keyed by
name. An absent field is absent; an empty list is written `[]`.

**Every refusal is one named rule.** The package holds its rules in a table
(`rules.go`), applied in a fixed order, so a document is refused for one
reason, named — the same reason whichever reader refused it. **The kits.**
`modulecontract.Fixtures()` (91 documents) ships
every accepted and refused document with the sentinel and the message a
refusal must carry and the rule it protects, and `Run(t, read)` drives a
reader's own entrypoint through them. `AllResolutionFixtures()` (117
configurations) does the same for resolution, driven by `RunResolution(t,
provider)`. **Both counts are pinned by a test**, and the resolution kit
is pinned by NAME and by cases-per-role as well: a documented count written by
hand was never checked at all, so deleting five of a role's cases tripped
nothing, and the sentence you are reading claimed a guarantee that did not
exist. A consumer passes the function it
actually reads the file with — the renderer's load, the publisher's merge,
the loader's parse — never this package's `Parse`, which proves nothing about
the consumer. The package's self-check (`TestEveryRuleIsProtectedByAFixture`)
proves the kit protects every rule: every rule is named by at least one
refused fixture, and the kit is run with each rule deleted in turn and must
fail on a fixture naming it — a rule that could be dropped silently is a rule
the kit does not protect.

**Adoption is a requirement, not yet a fact.** Nothing in this repository
drives a consumer through either kit, and until each consumer's own commit
lands, the second copies of this model still exist. What each one owes:

| Consumer | What it does, and where it stands |
| --- | --- |
| `codefly-dev/cli` | delete `pkg/modulecontract`, `docs/wire/` and `TestWireShapesArePinnedByDigest`; read every contract through `modulecontract.Load`; replace its `Values` adapter with a **duplicate-preserving** `Records(group) ([]Record, error)` provider that keeps repeated records, original key spellings and every secret classification; write through `Encode` rather than marshaling the model; and run **both** kits — the document kit and `RunResolution` — through the render's and the publisher's own entrypoints, never a test-only wrapper. The deletions are done at `b77f5806`, which PREDATES the `Records` API and the resolution kit, so that commit does not establish adoption of this head; the tested consumer commit is still to be named. Its cell model stays its own until core ships one. |
| the runtimes that publish contracts | run `modulecontract.Run` against the publisher's output, so a contract they emit is one this reader accepts. Not started. |

### A solution with no scoped consumption

`Manifest.ConsumptionContract` returns a named contract with empty bindings for
an unscoped solution: it asks for no operation. When a module also publishes a
contract, an empty consumption request does not compete with that contract;
nonempty competing requests remain an authoring conflict.
