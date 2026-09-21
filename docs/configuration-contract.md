# Producer Configuration Contract

Codefly defines the configuration contract. Infrastructure producers, including
infra-base, emit it. Core and CLI do not understand a producer's resource
inventory, database engine conventions, proxy mode, or credential naming scheme.

`resources.CellContract` schema `codefly/cell/v2` wraps the existing
`resources.Environment` model under `environment`. JSON keys inside that object
are exactly the workspace YAML keys. There is no second environment model.
`cell` and `coordinate` are optional opaque provenance labels.

Examples accepted by Core are in `resources/testdata/cells/managed-identity.json`
and `resources/testdata/cells/password-auth.json`. They are contract fixtures,
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
identity. `resources/testdata/cells/config-injection.json` is that whole shape.

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
