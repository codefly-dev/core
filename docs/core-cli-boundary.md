# Runtime configuration and deployment ownership

Core defines what a service receives at runtime. The CLI decides how a deployment
delivers it. Infrastructure producers resolve their resources into declarations
the CLI consumes. A Go import is not a reason for Core to own a declaration.

## Ownership

| Declaration | Owner | Reason |
| --- | --- | --- |
| Environment name, naming scope, fixture, configuration profile | Core | Selects the runtime configuration and agent invocation. |
| Configuration values, structured data, secrecy, reference resolution | Core | Shared by agents, local execution and SDK consumers. |
| Coordinate document, schema admission, required capabilities, target assertions | CLI | Imported and interpreted by deployment commands. |
| Cluster, registry, namespace, GitOps repository/revision/path | CLI | Selects and authenticates a deployment destination. |
| Ingress and application DNS suffix | CLI | Resolves deployment routing into ordinary network mappings. |
| Resource quotas and container defaults | CLI | Kubernetes admission and rendering policy. |
| Per-service value injection | CLI | Maps producer declarations to workloads; the resulting data is generic Core configuration. |
| ExternalSecret stores, remote keys, properties and templates | CLI | Specifies how Kubernetes delivers secrets; it is not the runtime secret model. |
| Workload principal, ServiceAccount annotations and pod labels | CLI | Binds a Kubernetes workload to an infrastructure identity. |
| Managed-service replacement and egress declarations | CLI | Changes the deployed graph and manifests, not the resource model agents share. |
| Database engines, IAM provisioning, cloud resource inventories | Producer | These resolve to declared values and deployment attachments before import. |

The existing `secrets` block selects Core's local secret resolvers. It is distinct
from `service-secrets`, which configures the Kubernetes External Secrets operator.
The former remains with its actual configuration consumer.

## Nested data

The generic primitive already exists: `codefly.base.v0.ConfigurationData` carries
format, bytes and a secrecy flag inside `ConfigurationInformation`. JSON objects,
arrays, numbers, booleans and null remain structured data. Flat environment values
remain strings. A JSON document must not be flattened into invented environment
key names, nor interpreted as a database-specific object by Core or the CLI.

The existing coordinate `service-config.values` dictionary is a deployment input,
not a restriction on the runtime configuration contract. Explicit structured
configuration belongs in that generic data path. Secret documents must remain
marked secret as a whole; GitOps carries references and never their resolved
contents. The versioned runtime carrier transports `ConfigurationInformation.data`
without flattening; Go SDK document accessors decode it with exact scope checks.
See [nested data delivery](configuration-contract.md#nested-data-delivery).

## Migration

1. Move the coordinate parser and deployment model into the CLI with their
   validation and fixtures. Keep `codefly/coordinate/v1` and its field spellings.
2. Change CLI consumers to use that model and project only runtime information
   into Core. Preserve host-owned YAML when Core loads and saves a workspace;
   preservation does not give Core authority to validate or interpret it.
3. Remove deployment declarations and their policy from Core. Keep shared agent
   rendering helpers generic; interpret identity declarations in the CLI.
4. Exercise producer JSON through import and rendered manifests, including a
   workload with configuration, secrets and identity but no managed services.
5. Land the consumer migration before releasing the Core removals. Update the
   CLI dependency to the reviewed Core change, then release the pair. Producers
   and Lodestar retain the wire format and adopt the released consumer.

Core must never depend on the private CLI module. Its checks exercise its generic
contracts; the CLI owns producer-admission and GitOps integration tests.

## Producer and consumer evidence

At infra-base `7011c60b99116c397e73a8ad11e9cf5afe6f2044`, the sanctioned
`obinctl coordinate-contract --all` emits this unchanged schema. The
`hosted-us-east1.lodestar.json` output supplies `staging/lodestar` and three
`accounts` values: `DATABASE_INSTANCE`, `DATABASE_READER_USER`, and
`DATABASE_WRITER_USER`. It supplies no workload identity, secret references,
cluster or GitOps target. Its values can be imported and rendered; that is not
proof that Lodestar reads them or authenticates successfully. The CLI preserves
that actual output as a regression fixture.

[Infra-base #1116](https://github.com/obin-ai/infra-base/issues/1116) owns the
remaining producer declarations and CLI qualification; #1099 still owns Azure
consumer bindings. [Lodestar #339](https://github.com/obin-ai/lodestar/issues/339)
must consume the reviewed CLI and producer output, not fill missing identity,
trust or delivery fields by hand. Its current Azure instructions still name the
retired schema, and it has not demonstrated the requested GCP model render.

## Existing work

Core #611 already merged and separates workload identity from managed services.
That behavior moves to the CLI and must render even without managed services.
CLI #766 contains parser migration and copy-isolation fixes; #768 contains the
producer-declared namespace fix; #769 propagates post-import validation failures.
Preserve those behaviors in the consolidated cleanup. The remaining Core work
from #610 (terminology), #618 (config-mount validation) and #620 (structured data)
is consolidated into #618 before the next Core release.

This is a fix at the owning components, not an alternative producer format or a
temporary database adapter. Import/render tests do not establish cloud login,
secret-store access or a successful deployed application boot.
