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
where a slot belongs is a schema error; an audience may instead be
`{self: true}`, the module's own principal. `Parse` decodes strictly (an unknown
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

**A binding may be optional.** A binding is mandatory by default: every
composition of the module fills its slots, or the render refuses it. A binding
marked `optional: true` is one a composition may leave out, for a capability
the module degrades without (ratings that answer "disabled" when no store for
them is composed). The rule is decided on the slots alone, in `Resolve`:

- **none filled** — every slot's key absent from its group, or supplied empty
  (the spelling a module's declared configuration gives a key nobody set): the
  resolution omits the binding and reports it in `Resolved.Omitted` with the
  reason, naming each unfilled slot. Its calls are never read, so the audience
  need not be composed. The delivered manifest carries no such binding, and
  nothing can be issued through it: the authority refuses an issue through a
  binding it does not know.
- **all filled** — resolved and delivered exactly as a mandatory binding.
- **filled in part** — resolved whole, so each unfilled slot is refused by name
  under its sentinel, the refusal saying the binding is optional and was filled
  in part: a misspelt key must never silently drop a binding.

A secret-classified record is a value, so a slot carrying one is filled (and
refused as a secret), never omitted around. `optional: false` is the default
and is never written.

**A binding may be addressed to a list of audiences.** A generic module reaches
whichever modules a composition wires to it — a store of comments reaches the
owner of each kind of thing commented on — so the audiences of one of its
bindings are a list only the composition can write. Such a binding declares
`audiences:` in place of `audience:`, a slot whose key is a `*-audiences` one
and whose value is the audiences comma-separated, with no space
(`robin,documents`, one line like every slot value). It means *this binding
may be exercised toward any of these audiences*, and `Resolve` delivers it as
**one resolved binding per listed audience**, sorted:

```yaml
bindings:
  - id: owner
    optional: true
    operations: [invoke, headless]
    audiences: {from: annotation-authorities/owner-audiences}
    calls:
      invoke: [/annotations.v1.TargetAuthorityService/Reach]
      headless: [/annotations.v1.TargetAuthorityService/ListKinds]
  - id: owner-review
    optional: true
    operations: [invoke]
    audiences: {from: annotation-authorities/owner-audiences}
    resource_kinds: {from: annotation-authorities/owner-resource-kinds}
    scope_ceiling:
      invoke: [read, review]
```

With `OWNER_AUDIENCES=documents,robin` and
`OWNER_RESOURCE_KINDS=documents=documents.versions,robin=robin.answers`, that
resolves to four bindings: `owner:documents`, `owner:robin`,
`owner-review:documents` and `owner-review:robin`. Each is its own unit of
authority, delivered in the manifest exactly as a single-audience binding is —
id `<binding>:<audience>` (`modulecontract.AudienceBindingID`), the audience,
and the ceiling resolved for **that** audience:

- **`calls`** are read from each listed audience's own declarations
  (`Callees.Requires(audience, call)`, once per audience), so two owners
  declaring one protocol's methods with different requirements get two
  different ceilings, and a call one listed audience does not serve is refused
  under `ErrUnresolvedCall` naming the binding, the call and that audience.
- **bare actions** are qualified per audience by `resource_kinds:`, a
  `*-resource-kinds` slot whose value is `<audience>=<kind>` pairs,
  comma-separated: each kind held to the literal-kind grammar, each pair naming
  a listed audience once, and a listed audience with no kind refused under
  `ErrUnresolvedSlot` by name.
- Never beside `audiences`: `audience` (a binding is addressed to one or to a
  list), `resource_kind` (one kind would name one module's vocabulary at every
  other), `{resource_kind, actions}` scopes in a ceiling (the same, spelled in
  the ceiling: `audiences-without-explicit-scopes`), `binding_key` (one key
  cannot install a binding per audience); and `resource_kinds` never without
  `audiences`. Each is its own named rule.

The derived id's `:` is outside the binding-id grammar a contract declares, so
it can never collide with a declared binding, and inside the opaque-id grammar
a delivered manifest holds binding ids to; the issuer looks a binding up by its
id and holds the issue to that binding's one audience, so a module is issued a
context toward `documents` by naming `owner:documents` (or naming the audience
and that binding) — no issuer change. A list that is not comma-separated
lowercase names, each once, is refused under `ErrAmbiguousSlot`
(`resolved-list-grammar`). An optional list-addressed binding composes with
the optional rule unchanged: the list key absent or empty (with its kinds key)
omits it; the list filled and its kinds key not is filled in part, refused by
name. A binding with `audience:` resolves exactly as before, under its own id.

**A binding may be addressed to the module itself.** A module that admits
work into itself on someone's behalf — a scheduler issued the capability an
occurrence is admitted under, from a delegation a person granted it — redeems a
binding whose audience is its own principal. That principal is already the
contract's (`principal:`), and every renderer holds it to the name the module
is composed under, so the audience says so instead of naming a configured
value that would restate an identity the composition already resolves:

```yaml
bindings:
  - id: occurrences
    operations: [invoke]
    audience: {self: true}
    scope_ceiling:
      invoke:
        - {resource_kind: scheduler.tasks, actions: [cancel, execute, start]}
```

`Resolve` delivers it under its own id with `Audience` = the contract's
principal, reading no configuration for it (`SlotGroups` lists none), and a
binding naming `calls` reads them from the module's own declarations
(`Callees.Requires(<principal>, call)`); every scope those calls require is
held to the module's own `scope_ceilings` exactly as a spelled-out scope is,
and one beyond them is refused under `ErrUnresolvedCall` naming the call and
the scope. It is held by named rules:

- `self` and `from` never in one slot (`slot-self-beside-from`), and `self` is
  written only as `self: true` (`slot-self-is-true`); a non-boolean `self` is
  malformed (`slot-decodes`), and the bare word `audience: self` is a literal
  where a slot belongs (`slot-is-a-reference`, whose refusal names the
  spelling).
- Only an audience may be the module itself — never `audiences`,
  `resource_kind`, `resource_kinds` or `binding_key` (`self-only-audience`).
- It asks for the module's own vocabulary: no `resource_kind` slot
  (`self-audience-no-resource-kind`), and every `{resource_kind, actions}`
  scope within the module's own `scope_ceilings`
  (`self-audience-own-scopes`). Bare actions need a `resource_kind` slot, which
  it cannot declare, so its scopes are spelled out.
- The module itself is no slot the composition fills, so it neither fills nor
  leaves unfilled an **optional** binding: an optional self binding is omitted
  by its other slots alone (a `binding_key`, say), and one with no other slot
  could never be left out, which is refused (`optional-binding-has-a-slot`).
- A solution's `api.consumes` entry asks at the module it names, so a
  `{self: true}` audience there asks for scopes beyond the solution's own
  (empty) ceilings and is refused by `self-audience-own-scopes`.

**A module may declare the methods it serves from a protocol it does not own.**
A method a module defines carries its policy as an option on its own proto; a
method of a protocol the module only serves (an OpenTelemetry collector's
`Export`) has no option the module can set. `foreign_methods:` lists each such
method by its full gRPC name with what a call must hold, in the module's own
vocabulary: every kind is one of its `scope_ceilings` and every action within
that ceiling, and the list names each method once, each requiring at least one
action. The render delivers them beside the methods its published contracts
declare, so the hop in front of the module decides them like any other.
An entry may name `service:` to select its serving service. The composition
requires that service to publish a gRPC or Connect listener; without a selector
it must find exactly one such service. This selects routing, not authority.

**A module declares owner-aware checking explicitly.** Top-level
`owner_aware: true` declares that the module enforces resource-owner selectors
in Work Context scopes. Omitted or `false` declares no such checking. The field
is a boolean; strings, including YAML 1.1 spellings such as `yes`, are refused.
`Parse`, `Encode` and `Resolve` preserve this declaration. Roles and
`installation_owned_scopes` never imply it. A solution must declare it before
the authority issues a person capability to it; the CLI carries the declaration
in the signed composition authority document's `owner_aware` field (field 7).
This declares an enforcement capability, not an owner identity or a grant.

**Installation declarations express machine intent and edge presence separately.**
`installation_role` is optional: absent or `""` leaves the legacy installer
selection unchanged; literal `none` declares no standing machine grant; any
other value must exactly name a declared role. This never assigns a role to a
person or team. `none` is reserved and cannot be a role name. Existing rules
still refuse every role that grants nothing and still require `installer` when
roles exist; selecting a role does not relax them. A bare module can declare
`none` without declaring roles.

```yaml
installation_role: none
edge_dependencies:
  - {module: robin, presence: required}
  - {module: annotations, presence: optional}
```

`edge_dependencies[].module` names the exact signed module principal (the
Authority domain's `Module.Principal`), each once, with mandatory
`presence: required|optional`. It grants no authority and declares no operations
or scopes. The host resolves the principal through its existing catalogue to
the live target and binding; a principal is not itself a target UUID. Route
aliases, binding IDs and release strings are never fallbacks. Core does not
require a principal to exist in a local composition. Entries are explicit;
none are inferred from bindings. Absent and empty lists mean the
same thing. Selector and module values with surrounding whitespace are refused,
never trimmed; declaration fields must be strings.

`Parse`, `Validate`, `Encode` and `Resolve` use the existing contract rules and
carry accepted values unchanged. The new fields use `omitempty`, so contracts
that declare neither retain their previous encoded bytes. CLI owns signed
projection; Authority owns honoring `none` without a synthetic role or standing
grant; SaaS owns dependency review and activation. Parser acceptance alone does
not establish that those consumer paths support installation.

**A unit a person uses declares its roles beside its own vocabulary.** A
solution is reached by people, and who may do what on an installation of it is
a role the installation grants, never the organisation's. `roles:` lists each
role by name with the scopes it grants, `{resource_kind, actions}` with the kind
named literally:

```yaml
scope_ceilings:
  - resource_kind: wiki.page
    actions: [read, write, publish]
roles:
  - name: installer
    scopes:
      - {resource_kind: wiki.page, actions: [read, write, publish]}
  - name: viewer
    scopes:
      - {resource_kind: wiki.page, actions: [read]}
      - {resource_kind: documents, actions: [read]}
```

A role grants the unit's **own kinds**, each action within its
`scope_ceilings`, and the **permissions at another module it consumes**: a
scope one of its bindings spells out (`{resource_kind, actions}`) for `invoke`
or `lookup`, the operations a person causes. A `headless` binding's scopes are
never grantable (no person is in one). A bare action or a call names its kind
only when the composition resolves it, so a role never spells one out; it
**names the binding** instead:

```yaml
roles:
  - name: viewer
    bindings:
      - {binding: evidence, operation: lookup}
```

A `bindings` entry grants what that binding carries for `invoke` or `lookup` as
`Resolve` derives it — the scopes its calls require, read from each audience's
declarations, or those it spells out — at each audience it is delivered to, so a
unit grants a permission at another module without naming that module's kinds.
It names a declared binding (`role-binding-known`), an operation a person causes
(`role-binding-person-operation`) that the binding declares
(`role-binding-declared-operation`), each binding operation once per role
(`role-binding-unique`). A grant through an optional binding the composition
leaves out is left out with it.

`Resolved.Roles` addresses every grant: per audience, the scopes the role grants
there. A role scope names no audience, and is still never ambiguous: it is held
to the unit's own ceilings or to a scope a binding spells out, and is addressed
to the one audience that underwrites it — the unit's principal for its own kind,
that binding's resolved audience for a consumed one (a ceiling that spells
scopes out belongs to a binding with one audience, never a list). A scope two
audiences underwrite is refused under `ErrAmbiguousSlot`, naming both; the role
then names the binding it means in `bindings`.

**A role may grant a scope only on what the installation owns.**
`installation_owned_scopes` lists scopes the role grants only on the resources
the installation it is held on owns — those created or attached through it —
spelled like `scopes`:

```yaml
roles:
  - name: viewer
    scopes:
      - {resource_kind: wiki.page, actions: [read]}
    installation_owned_scopes:
      - {resource_kind: documents.collection, actions: [read]}
```

Each names a kind and actions and never resource ids, since ids are each
installation's own: the entry has no field for them, so one naming
`resource_ids` is refused as an unknown field. It is held to every rule a role
scope is — kind and action grammar, each kind once in the list, at least one
action, within the unit's own ceilings or a scope a binding spells out for
`invoke` or `lookup` — and refused under the same rule names, its message
saying "installation-owned". A role may grant through it alone, and a kind may
appear in both lists, each with its own actions. `Resolved.Roles` addresses it
exactly as a literal scope, in `InstallationOwnedGrants`. The authority
resolves it per installation into the same scope limited to the ids that
installation owns of the kind; a kind it owns nothing of grants nothing, never
the whole kind.

One of the roles is `installer`, which whoever installs the unit holds; each
role is a lowercase name declared once and grants at least one kind or binding,
each kind named once with at least one action, held to the same kind and action
grammar as every other scope here. Each of these is its own named rule
(`role-name`, `role-unique`, `role-grants`, `role-kind-name`,
`role-kind-unique`, `role-kind-has-action`, `role-installer`,
`role-within-request`). A bare module declares no `roles`, and omits the key:
its access is the organisation's roles. The CLI carries the roles into the
authority's document delivered with the unit's manifest
(`AuthorityManifestV1.roles`); a role's `bindings` reach it only through
`Resolved.Roles`, which needs the callee declarations its binding's calls are
read from. This contract is their one declaration site.

**A module may require a policy on every method it publishes.**
`require_method_policies: true` holds each published method — every RPC of its
published contracts, every route of its published OpenAPI, every
`foreign_methods` entry — to an explicit policy (requirements or
`any_checked_caller`) wherever the module is delivered, so an unmarked method
is refused by name before anything is rendered. Absent, the deliverer decides
from what the host consumes: a host that takes method policies refuses an
unmarked method before delivery, one that does not leaves it to the hop, which
refuses an undeclared method per call. It is a boolean; any other value is
refused.

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
`modulecontract.Fixtures()` (144 documents) ships
every accepted and refused document with the sentinel and the message a
refusal must carry and the rule it protects, and `Run(t, read)` drives a
reader's own entrypoint through them. Accepted documents declare
`principal: assistant`; a consuming renderer uses module `assistant` and passes
the fixture bytes unchanged, preserving its principal/identity guard.
`AllResolutionFixtures()` (120
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
