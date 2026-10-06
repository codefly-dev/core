# Versioned workspace composition

A product can select one versioned platform workspace and its solutions without
copying the platform's module inventory:

```yaml
name: product
layout: modules
workspaces:
  - name: platform-core
    source: team/platform-core
    version: 1.0.0
solutions:
  - name: wiki
    source: team/solutions
    module: solutions/wiki
    version: 2.0.0
  - name: lastlogin-go
    source: team/solutions
    module: solutions/lastlogin-go
    version: 2.0.0
```

The CLI acquires workspace releases; Core accepts a context-scoped
`WithWorkspaceResolver` callback and never fetches source. A workspace reference
may select a repository subdirectory with `workspace`, or use `path` instead of
source/version for explicit local development.

Core expands the effective module graph in memory. The imported workspace owns
its module pins; the product's `solutions` entries use the existing module
reference model. Duplicate names, cycles, flat-layout imports and unresolved
releases fail loading. Saving the product retains its references, not copies of
the imported modules. `ModuleDeclarationDir` identifies the owner of each
module's resolution and trust policy for hosts that implement acquisition.

For the product-selected configuration profile, imported workspace groups are
inherited. Product-owned groups override imported groups. Conflicting sibling
workspace groups fail unless the product supplies that group. Existing module
configuration fallback remains below workspace-owned configuration. Imported
environments never override the product's deployment target.

### Overriding a composed module's group

A composed module brings its `configurations/<profile>/*` into the consuming
workspace's configuration space, so the module's services satisfy their
workspace-configuration-dependencies from the solution alone. A solution
overrides one of those groups by declaring a group of the same name — and it
overrides it **per key**:

```
host/configurations/local/app-config.env     solution/configurations/local/app-config.env
    CONFIG_DIR=/etc/app                          CONFIG_FILE=/etc/app/solution.yaml
    CONFIG_FILE=${profile}
    CONFIG_MODE=${profile}
```

`CONFIG_FILE` is the solution's. `CONFIG_DIR` is still the module's default, and
`CONFIG_MODE` is still a value each profile must supply — so this composition is
refused, naming `app-config/CONFIG_MODE`, instead of running without it. A
solution overriding one key is never required to restate the group.

**The module's group is the declared set of the group's keys**, exactly as a base
profile is for the profiles derived from it, and two rules follow:

- a key only the solution carries is refused, naming it — the same rule a derived
  profile gets over the profile it derives from. The group is delivered to the
  module's services, which read the keys the module declared; a key its
  declaration never mentions is a value nothing reads. A solution that needs a
  key of its own declares a **group** of its own name, which reaches every
  service of the composition.
- a `${profile}` the override does not discharge is still owed, and an override
  that discharges one with an **empty value** is refused naming the key. An empty
  value is not a value: accepted, it would deliver the key as the empty string
  every reader of a missing key gets, which is what the declaration exists to
  refuse. A key for which nothing is a legitimate value is declared with a
  default by the group's author, not with the marker.

  This second refusal is **this boundary's own**: profile derivation does not
  carry it today, so a derived profile that empties a `${profile}` its base
  declares is still accepted. The two layers are not the same situation — the
  profiles of one workspace are written by the author who wrote the declaration,
  while across this boundary the author discharging the marker is not the author
  who declared it, and cannot be refused later by a reviewer of the module.

One key declared twice **in one profile** — a repeated line, or two files
consolidated into one group, under either spelling — is refused where it is
written, naming the profile and both spellings, rather than resolved by which
line came last. It is refused per profile rather than at this boundary alone,
because a derived profile's duplicate would otherwise be flattened into one
value before anything could see it, and *which* value reached the workload would
be decided by the order of two lines. A key declared once in a base profile and
once in a profile derived from it is the derivation itself, not a duplicate.
Keys are matched the way every other configuration lookup matches them: case
insensitively, with `-` and `_` equivalent. A structured document (a `.yaml` group) has no keys to
overlay, so the solution's document replaces the module's whole; an empty one
does not discharge a document declared per profile, and a boundary that turns a
document into key/value pairs is a conflict.

**An overridden group stays composed.** Its provider is still the module, so it
reaches the services that declared it as a dependency rather than every service
of the composition. Overriding a key of someone else's group is not a statement
about every service in the run — and a run-wide injection of an overridden group
would carry the module's own keys, the ones the solution never wrote, into every
service of it. Run-wide delivery has two ways to be asked for, and both say so:
a group under a name no composed module provides is the composition root's own,
and an operator's `--set` is attributed to the run, which stays composition-root
even on a name a composed module provides.

When two composed modules define one name differently, the name is ambiguous and
unavailable until the solution declares it; a solution that does declare it
resolves the ambiguity rather than being reported it — there is no single group
to overlay onto, so its declaration stands whole as the composition root's own.

This per-key overlay is the consuming workspace's **own** declaration over a
composed module's group. A workspace-level group an imported workspace also
carries — the product model above — still replaces a module's group of that name
whole: an imported declaration is not the product's to amend, and overlaying the
product's own onto the module's group where an imported workspace declares the
name too would rank a module default above the imported declaration it sits
below. Deciding the product model's precedence per key is a separate change.

### Configuration profile chains

An environment reads `configurations/<profile>/` (and each service's
`dns/<profile>/`) for one profile: its `configuration-profile`, or its name.
`configuration-profiles` declares an ordered chain instead, e.g.

```yaml
environments:
  - name: staging
    configuration-profiles: [staging, local]
```

Each configuration location — the workspace's own directory, each composed
workspace's, each composed module's, each service's configurations and DNS —
resolves on its own to the **first** profile in the chain it holds, and is read
from that profile alone. Profiles are never merged, so a workspace holding
`configurations/staging` reads none of its `configurations/local`, while a
composed module that ships only `configurations/local` keeps supplying those
defaults. The first profile is the environment's own; it is where authored
configuration is written.

Nothing falls back unless the environment declares the chain. A module's
local defaults can carry development-only values (fixture identity, dev
secrets), and an implicit fallback would hand them to a deployed environment
unannounced; the chain makes that inheritance a reviewed line in the workspace.
`configuration-profile` and `configuration-profiles` are mutually exclusive.

### Profile derivation, and values supplied per profile

A chain selects a whole directory, which is why it cannot express "the same
groups, with three values different". Restating every group in a second
directory is how a deployed profile ends up hand-copied from the local one and
drifting from it key by key — and why a deployed environment borrows
`configuration-profile: local` rather than naming its own situation.

A profile directory declares what it derives from, beside the values, in
`configurations/<profile>/profile.codefly.yaml`:

```yaml
# configurations/deployed/profile.codefly.yaml
derives-from: shared
```

The profile it names is read from the SAME location first, and this profile's
files are overlaid on top — per value, matched the way every other configuration
lookup matches (case insensitively, `-` and `_` equivalent). So one declared set
of groups serves every environment and each profile restates only what differs:

```
configurations/shared/work-context.env      # declared once
configurations/local/profile.codefly.yaml   # derives-from: shared
configurations/local/work-context.env       # audience=http://localhost:8080
configurations/deployed/profile.codefly.yaml
configurations/deployed/work-context.env    # audience=https://api.example.com
```

**The base profile is the declared set of a group's keys.** A derived profile
overrides a key the base declares; a key it *introduces* into that group is
refused, naming the key and the remedy. Such a key is a difference between
environments that the declared set never mentions — every profile that does not
carry it renders the group without it, and a missing key reads as the empty
string with no error anywhere, which is the until-runtime failure the declared set
exists to prevent. Declare it in the base, with `${profile}` when each profile
supplies its own.

A group a derived profile introduces *whole* is a different case and is allowed:
it is absent from every profile that does not carry it, and a consumer that
declares a group nothing provides is told so by name.

A derived declaration replaces the whole value rather than merging into it, so a
profile that makes a shared default secret, or replaces it with an assembled
template, says so in one place. One profile declaring the same key **twice** is
refused instead, naming the profile and both spellings: overlaying the second
onto the first would deliver whichever the file named last, which is not a
statement either declaration makes. A structured document (a `.yaml` group) has no
keys to overlay, so a derived profile's document replaces the one it derives from
— overlaying one *field* of a document per profile is not supported, and such a
group is declared per profile whole or stays shared; turning a document into
key/value pairs across layers is a conflict. A single-file declaration — a
service's `dns/<profile>/dns.codefly.yaml` — comes from the most derived profile
that ships one.

A derivation that cannot be honoured fails the read: a profile that is not there
at that location, a chain that closes on itself, one read from more than eight
profiles, a declaration carrying a key nothing reads, or an empty or absent
`derives-from` in a file that exists for no other purpose. None of them degrades
to reading the selected profile alone, which would hand a deployed environment a
group stripped of everything its author expected it to start from.

Derivation and the chain do not overlap, and they compose. Derivation is declared
by the profile's author, beside the values, and says what a profile starts from;
the chain is declared by the workspace that consumes a composition, and says
which profile a location reads when it does not hold the environment's own.

#### `${profile}`: a value each profile must supply

Some values cannot be shared — an audience, an authority, an external account.
A group declares that where the value lives, instead of a value:

```env
# configurations/shared/work-context.env
audience=${profile}
```

A value still carrying `${profile}` once the selected profile and everything it
derives from have been read is owed. It fails the load, before anything is built,
started or rendered, naming the group, the key, the profile and the file that
declared it — so a deployed environment that forgets one is refused rather than
rendered with the base profile's development value, or with the key missing.
A failure of that class is otherwise invisible until a client dials the address.
An invocation-scoped override (`--set`) supplies one like any other value.

The marker is looked for anywhere in the value, so a half-written declaration
(`https://${profile}/token`) is owed too. A structured document is scanned as a
whole: it has no key model, so the marker anywhere in its content means each
profile supplies that whole document, and the requirement names the group with no
key.

Prefer a value the system resolves over one typed per profile: an address is
`${endpoint:<module>/<service>/<endpoint>}` (with `|authority` for host:port),
declared once and resolved per run against that run's network mappings, so local
and deployed differ by the mappings rather than by a second copy of the group.

A render that resolves those references states what it is rendering: the run's
network mappings, and its run set (`configurations.Manager.WithNetworkMappings`
and `WithRunProducers`). Both the per-consumer path and the run-wide path — the
one the composition root's own groups take — drop a reference a consumer cannot
see, and dropping is a judgement about one consumer of a render. A caller that
states no run set has said nothing, so a reference it cannot resolve is an error
there rather than an address that quietly leaves the workload.

The run-wide read is also judged per consumer in *which* groups it resolves. A
consumer that receives only some of the root's groups — a service a root
credential is withheld from — names them
(`GetCompositionRootWorkspaceConfigurations(ctx, names...)`), and only those are
resolved: a reference that is a fault rather than an omission (ambiguous, or
without an instance for the consumer's access) in a group it does not receive
cannot refuse it. With no names every root group is resolved and the first
fault refuses the read, as before; a name the root does not provide run-wide —
a composed module's group, an ambiguous name, a name nothing loaded — refuses
the whole read by name (`configurations.ErrNotACompositionRootConfiguration`)
rather than answering with less than was asked. Zero names is the whole set, so
a consumer whose received set is empty has nothing to read and does not call.

This changes resource composition only. Deployment declarations and artifact
acquisition remain CLI responsibilities; no agent protocol changes are needed.
