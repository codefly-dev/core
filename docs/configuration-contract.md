# Runtime configuration and deployment declarations

Core owns the generic configuration contract shared by agents and services:

- `codefly.base.v0.Configuration` identifies an origin and runtime context.
- `ConfigurationInformation` groups string values or structured data.
- `ConfigurationValue` marks each string value as public or secret.
- A secret `ConfigurationValue` may carry a `ConfigurationValueTemplate` instead
  of a value: literals around references to the declaring producer's own
  configuration values, each reference stating an escape. Core assembles it
  wherever it delivers values itself — `ProducerConfigurationValueLookup` scopes
  resolution to that one producer, so a reference cannot name another service's
  secret, and `EvaluateConfigurationValueTemplate` is the byte-for-byte reference
  semantics a renderer must reproduce. A reference that is absent, empty or
  itself an assembly fails; it never yields an empty credential. Host support is
  declared as the `configuration-value-template/v1` operation contract.
- `ConfigurationData` carries a format, bytes and a secrecy flag. Nested JSON
  belongs here without database, cloud or Kubernetes-specific fields.
- The configuration loader resolves configured secret references without
  persisting the resulting secret values.

The CLI owns the producer-facing `codefly/coordinate/v1` deployment document,
its strict parser and its Kubernetes projection. See the
[CLI contract](https://github.com/codefly-dev/cli/blob/main/docs/configuration-contract.md).
Cluster, registry, namespace, ingress, GitOps targets, quotas, managed-service
replacements, ExternalSecret delivery and workload identity attachments are
deployment concerns. Infrastructure producers resolve their resources into that
contract; the CLI does not translate private infrastructure inventories.

Core's workspace and environment loaders preserve host-owned YAML extensions so
saving runtime configuration cannot erase deployment declarations. Core does not
interpret, validate or send these extensions to agents. The host must admit its
own declarations before acting on them.

The local `secrets` selector remains in Core because its configuration loader
actually consumes it. It is distinct from the CLI's `service-secrets` projection
into Kubernetes ExternalSecrets.

## Nested data delivery

`ConfigurationAsEnvironmentVariables(configuration, environment, secret)` returns
variables and an error. It emits flat strings unchanged and structured data through
the versioned `codefly/configuration-document/v1` JSON envelope. The scope contains
the exact origin, configuration name, environment and secrecy flag. Its environment
key hashes those identity strings without case or punctuation normalization.
Public and secret documents use separate namespaces; the decoder verifies the
complete requested scope and rejects unknown envelope fields and trailing data.

JSON objects, arrays, numbers, strings, booleans and null retain their types and
numeric precision. JSON-compatible single-document YAML is converted to JSON.
Unsupported formats, malformed input, absent scope and documents or encoded
carriers exceeding 64 KiB fail explicitly. Error messages exclude content. The
directory loader's existing `.yaml` and `.secret.yaml` names are unchanged.

A configuration's **name** is its file stem (`datasource-oauth.env` declares
the group `datasource-oauth`), and it is a structural segment of every carrier
built from it, spelled by `NameToKey` with `__` separating it from the key. A
stem that carries consecutive separators (`--`, `__`, `-_`, `_-`) is refused
where the file is loaded, public and secret files alike, because its spelling
would carry `__` of its own: `policy--auth.env` and `policy__auth.env` both
spell `POLICY__AUTH`, and a carrier built from either reads as the group
`policy` with the key `AUTH__…`. Single dashes are the name's grammar, and the
same grammar holds a **module name** and a **service name**, the other
structural segments of a carrier (`public--auth` would spell `PUBLIC__AUTH`
and shift a service carrier's first key segment into its structure); both are
refused where they are loaded. No configuration file, module or service in
the fleet's checkouts carried such a name when the restriction landed
(2026-10-13); one that does fails to load with a message naming it and its
spelling. Every reader of a carrier's key uses
`resources.ConfigurationCarrierKey`, and every classification of a carrier
name — the resources classifier, the restricted-manifest validator, a
deployment consumer's scan — goes through `resources.IsSensitiveEnvironmentVariable`,
so a secret carrier is sensitive whatever its key, a configuration carrier is
read by its complete key — the outermost carrier first, so a key that spells
an address carrier is still a key — and a group's or service's name never
classifies a public value.

`EnvironmentVariableManager.Configurations` never emits secret documents;
`Secrets` returns them separately with error propagation. Raw configuration
injection refuses structured data because it has no document identity. Restricted
rendering refuses resolved structured secrets, including configurations
containing no flat secret keys. It must carry declared external references.

A restricted render carries no secret values, so it does not assemble a
templated value: `DeliverConfigurationTemplatesByReference` omits it, and the
render is admitted only when it declares a `KubernetesSecretKeyReference` for
that value's carrier — otherwise the workload would boot with the credential
silently absent. The assembly is then delivered by the environment's secret
store, which is why the manifest contract admits an `ExternalSecret`
`spec.target.template` whose every value is an assembly over that
`ExternalSecret`'s own declared `secretKey`s, and refuses `templateFrom`,
`metadata`, a template combined with `dataFrom`, and any value that inlines
text rather than referencing a declared key.

SDK-Go exposes `ConfigurationDocument`, `SecretDocument` and workspace equivalents
plus typed decoding methods. This transport is generic runtime behavior, not a
reason to restore deployment or database models to Core. Existing flat JSON strings
remain strings and are not silently converted into documents.

A flat value larger than 32 KiB is delivered as a file whose path the
environment carries, so no value is lost to a platform's environment limit;
see [Runnable binding delivery](runnable-binding-delivery.md).

See [the boundary and migration decision](core-cli-boundary.md).

### Source origins for workspace diagnostics

`ReadWorkspaceConfigurations` returns value-free `Origins` alongside `Infos`.
The public evidence types are generated `codefly.base.v0` protobuf messages
from `proto/codefly/base/v0/configuration_evidence.proto`; private trace state
never crosses that boundary.
Each origin identifies a final group/key's supplying file; structured documents
have `Document=true` and an empty key. Object identity preserves equal-valued
overrides through profile selection and composition, including retained defaults
cloned during workspace per-key overlays. Resolver behavior is unchanged. Only
surviving results have origins, never values, secret URIs or content hashes.
Origins cover disk composition before invocation overrides, reference
resolution and injection. They are not a complete input fingerprint, history of
shadowed candidates or evidence of deployed/runtime values.

`Decisions` additionally records executed workspace-key, document or whole-group
replacement of module defaults, with module identity, rule, selected origins and
shadowed origins. `Rule` is the generated protobuf `ConfigurationRule` enum,
whose named values are owned by the schema rather than copied string literals.
If a module offers a replaced group from both its repository root and its own
directory, one decision identifies that module/group and keeps both shadowed
source locations.
It also records workspace-group-over-imported-group decisions, naming both
workspaces. Nested decisions retain intermediate choices; `Final` follows the
actual selected group object to distinguish them from the final collected result.
This hook runs at the precedence decision, including equal-valued replacements;
it does not infer changes by comparing values. Derived-profile decisions also
record individual-key or whole-document replacement. Their final marker follows
the selected value/document object, preserving intermediate history even for
equal values. This is not a complete audit trail of conflicts or rejected offers.
In particular, the resolver deduplicates equal groups from sibling imports (and
equal composed-module offers), retaining the first supplied object. Origins name
that retained source; no replacement decision or second origin is emitted for
an equal duplicate. The trace follows that choice without comparing values
itself, and does not enumerate every declaration that agrees with it.

`ProfileSelections` records each configuration location's ordered candidate
profiles, whether a candidate directory exists, and the actual derivation layers
read base first. It records absent locations too. These are directory selection
and explicit derivation, not per-file fallback, and do not establish the current
identity of previously collected inputs.

Every evidence path (`File`, `Location` and `Layers`) is slash-separated and
relative to the consuming workspace passed to `ReadWorkspaceConfigurations`,
including paths for imported workspaces and composed modules. External sources
may begin with `../`. The absolute composition root is not included in the
evidence; the caller already has `workspace.Dir()` and can resolve a relative
path locally. Moving the same composition tree between host directories leaves
its evidence unchanged. Source, group and key names remain visible metadata.

The public diagnostic read collects this evidence. Ordinary
`ConfigurationInformationLocalReader.Load` uses the same resolver without
allocating an origin trace or projecting evidence it would discard, then applies
the usual invocation overrides, reference resolution and provisioning.

Only the three evidence collections (`Origins`, `Decisions` and
`ProfileSelections`) have this value-free contract. `Infos` still carries the
configuration values and documents for provisioning; serializing the entire
`WorkspaceConfigurations` result exposes them. Changing plaintext, inline
secrets, secret references or document contents without changing their source
selection leaves the serialized evidence unchanged, including shadowed sources.

## Public module artifacts per load

A profile can declare `module-artifacts.codefly.json` using
`ProfileModuleArtifactConfigurations` protobuf JSON:

```json
{"schemaVersion":1,"bindings":[{"configuration":"runtime-installations","key":"INSTALLATION","source":{"module":"documents","path":"public/installation.json"}}]}
```

The composition author explicitly asserts these selected bytes are public for
this destination. Confinement and SHA256 do not establish publicness: a mistaken
assertion can disclose content and a dictionary-testable digest. There is no
secret scanner, publicness catalog or inferred filename rule.

Use the existing first-existing profile and explicit base-first inheritance.
Bindings enter before precedence. An inherited reference may be replaced by a
reference; ordinary literals, secrets, templates, structured replacements and
invocation overrides cannot overwrite a bound destination, even with equal
bytes. Only the exact non-secret `${profile}` slot with no template can yield to
a binding. Moving a literal to a reference requires an explicit source edit
removing the literal, including conflicting inherited offers. Other keys retain
ordinary precedence, ownership and root/module delivery scope.

`ConfigurationInformationLocalReader.WithModuleArtifactResolver` supplies a
`ModuleArtifactResolver`; pure `ReadWorkspaceConfigurations` takes the same
optional resolver. Core invokes it once per effective module/path in each load,
copies exact bytes and generates `resolved_module_artifact` evidence on the
value. The resolver must use the final selected module and preserve its selection
kind and provenance. Explicit path/worktree selection can lack a release
receipt; receipt-required materialization must not downgrade to local because
its overlay resolves to a directory. It must confine bounded, nonblocking regular
file reads and refuse traversal, special files and observed read instability.
Core neither downloads modules nor calls providers to acquire these artifacts.

The declaration document is limited to 64 KiB. Artifact values use the existing
inclusive `resources.FileCarrierThreshold` (32 KiB), valid UTF-8, no NUL and no
recognized endpoint/profile interpolation. Whitespace and newlines are preserved.
There is no compaction, file/mount fallback or `ConfigurationData` substitution.
Missing sources/resolvers and conflicting offers fail; diagnostics omit values.
Runtime still owns installation semantics.

`module_artifact` is unresolved declaration field 5; generated field 6 is
`resolved_module_artifact`, with reference and `SourceIdentity` SHA256/byte count.
Authored evidence is refused. Core emission validates the evidence against the
actual non-secret, non-template Value before filtering. Unresolved declarations,
stale digests, wrong sizes and unsupported identity algorithms refuse. Typed
clones retain evidence. This is an observation, not a signature against malicious
in-process callers or proof of release authenticity.

`resources.ValidateModuleArtifactDelivery(baseline, contributions, overrides)`
checks all ordered typed offers before merging and the exact once-computed
process override map. Baseline is initial selected delivery, or the detached
actual successful Init payload later; it is comparison state, not a request to
emit again. Every additional response/dependency offer must remain in
contributions. A second occurrence matching a bound destination refuses,
including equal, annotated, unannotated, public/secret and file-carrier aliases.
Ordinary unbound/unbound precedence remains unchanged. The owner matcher keeps
origin/group/key scope; a trailing-key comparison is insufficient.

CLI must validate Init before dispatch, response contributions before exposure,
Start against retained Init (not a later manager load), and excluded-root export
against its actual selected delivery. Its complete output preflight includes
configuration, runtime overrides, identity, fixtures and endpoints. The raw
output writer additionally refuses invalid SDK key grammar and LF/CR/NUL in
**every** emitted value before the first batch write, including unbound records,
and enforces the same rule at the lower-level writer. It serializes the same
strings it checked; no codec or normalization is implied. Multiline direct
process delivery remains supported. Core's schema/value checks do not qualify
the CLI writer or SDK roundtrip.

Every later normal load may change, add or remove references. Planning and check
are observations of their own loads; generated credentials remain visible to the
normal post-provisioning load. Extend retains ordinary reload behavior. Already
running workers keep their actual delivery: no flow-wide freeze, watcher,
automatic adoption or composition-wide transaction is introduced.
