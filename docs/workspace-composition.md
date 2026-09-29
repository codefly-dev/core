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

A group only a derived profile declares is added; a group only the base declares
is inherited whole. A derived declaration replaces the whole value rather than
merging into it, so a profile that makes a shared default secret, or replaces it
with an assembled template, says so in one place. A structured document (a
`.yaml` group) has no keys to overlay, so a derived profile's document replaces
the one it derives from; turning a document into key/value pairs across layers is
a conflict. A single-file declaration — a service's `dns/<profile>/dns.codefly.yaml`
— comes from the most derived profile that ships one.

A derivation that cannot be honoured fails the read: a profile that is not there
at that location, a chain that closes on itself, one deeper than eight profiles,
or a declaration carrying a key nothing reads. None of them degrades to reading
the selected profile alone, which would hand a deployed environment a group
stripped of everything its author expected it to start from.

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
(`https://${profile}/token`) is owed too. The content of a structured document is
opaque to Codefly and is not scanned for it.

Prefer a value the system resolves over one typed per profile: an address is
`${endpoint:<module>/<service>/<endpoint>}` (with `|authority` for host:port),
declared once and resolved per run against that run's network mappings, so local
and deployed differ by the mappings rather than by a second copy of the group.

This changes resource composition only. Deployment declarations and artifact
acquisition remain CLI responsibilities; no agent protocol changes are needed.
