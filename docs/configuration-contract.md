# Producer Configuration Contract

Codefly defines the configuration contract. Infrastructure producers, including
infra-base, emit it. Core and CLI do not understand a producer's resource
inventory, database engine conventions, proxy mode, or credential naming scheme.

`resources.CoordinateContract` schema `codefly/coordinate/v1` wraps the existing
`resources.Environment` model under `environment`. JSON keys inside that object
are exactly the workspace YAML keys. There is no second environment model.
`coordinate` is an optional opaque provenance label; the document is named for
its subject, and "cell" is not a word the model has.

The previous spelling `codefly/cell/v2` is accepted for one release and reported
by `UsesDeprecatedSchema`. The alias is on the name, never on the shape: such a
document is parsed by the same grammar, and no section is back-ported to it.

Examples accepted by Core are in `resources/testdata/coordinates/managed-identity.json`
and `resources/testdata/coordinates/password-auth.json`. They are contract fixtures,
not claims that a particular infrastructure producer already emits this format.

Producers must supply:

- The selected environment name and namespace. Import refuses a different target
  rather than reusing credentials or delivery paths resolved for another target.
- Each managed service under its explicit service key, with its endpoint and port.
  Multiple services are supported without assigning the first one to `store`.
- Explicit secret references, including the target Secret name, remote key,
  optional property and secret store. No declaration means no reference is added;
  it does not assert that the service is passwordless.
- Any workload identity principal and opaque annotation/label attachments.
  Identities and secret references can coexist. Core does not infer an auth mode.
- Any application secret mappings through the existing `service-secrets` model.
- Any resolved non-secret values through `service-config`, keyed by consuming
  service and then by the exact key the service reads. The producer resolves the
  value; Core carries it verbatim and derives none of it. A declared service with
  nothing to inject, or a value that resolved to empty, is refused at load.

Values and secret references are two blocks, not one dictionary of either-or
entries: a resolved value belongs in `service-config`, a reference in
`service-secrets`. One service declaring the same key in both is refused rather
than resolved — both render an entry of that name into one container, where one
silently overwrites the other. The refusal compares explicit `remote-keys`; a
`defaults` template covers whichever of a service's own keys are declared
secret, which the environment block alone cannot see.
- Resolved delivery repository, branch and path when declaring a GitOps target.
  Core neither appends the namespace nor chooses a branch.

Local configuration can declare `configuration-profile` and `secrets` without a
cluster or registry. Import is configuration admission, not deployment approval:
CLI still validates the selected operation, service graph and deployment target.
Local secret resolution and deployed secret projection remain separate consumers
of the existing configuration model; secret values do not belong in descriptors.

Unknown fields and required capabilities are rejected. A producer declaring a
workload identity uses `requires_capabilities: ["managed-service-identity"]`.
Proxy containers, image choices and loopback routing are not part of this contract.

## Configuration and secret injection

Injecting a workload's configuration and secrets needs four declarations and no
others: the target (`name`, `namespace`, `cluster.context`), resolved values
under `service-config`, secret references under `service-secrets`, and a workload
identity. `resources/testdata/coordinates/config-injection.json` is that whole shape.

A producer emitting for this path populates nothing else. `managed-services`,
`registry`, `ingress`, `resource-quota` and `dns` serve other flows and are
correctly absent here; a producer's own inventory — database engines, store
isolation tiers, cloud and location, operator cluster access — has no home in
this contract by design, and a non-secret value does not become one by being
routed through a secret store to find somewhere to live.

## Migration

Version 1 is rejected, not converted. Its database-to-`store` translation,
`password_auth` inference, manufactured `secret-store`/`<namespace>/store` handoff
and implicit delivery paths are removed. The producer must emit the resolved
Codefly environment directly, and the CLI import fixtures must move with it.
No compatibility shim reads infra-base's private format. Producers and CLI must
qualify against the published Core contract before release.

Workload identity projection validates a staged manifest tree and atomically
exchanges it with the destination on Linux and macOS. Cancellation before the
exchange leaves the destination unchanged; after publication the complete tree
is committed. Filesystems without atomic directory exchange fail without a
per-file publication fallback. Existing file and directory permissions survive.
