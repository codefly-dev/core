# Runtime configuration and deployment declarations

Core owns the generic configuration contract shared by agents and services:

- `codefly.base.v0.Configuration` identifies an origin and runtime context.
- `ConfigurationInformation` groups string values or structured data.
- `ConfigurationValue` marks each string value as public or secret.
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

Structured data exists in the protobuf model and local file loader, but that
does not establish SDK support. The current `ConfigurationAsEnvironmentVariables`
bridge emits only `ConfigurationValue` entries; the Go SDK's
`InjectConfigurations` uses that bridge and drops `ConfigurationInformation.data`.
Its public configuration accessors return strings.

Tracked in [Core #620](https://github.com/codefly-dev/core/issues/620) and
[SDK-Go #34](https://github.com/codefly-dev/sdk-go/issues/34). Neither is supplied
by the deployment ownership migration.

A complete nested JSON API requires a generic carrier and SDK accessors that
preserve objects, arrays, numbers, booleans and null, with separate secret
handling. That is a runtime/SDK change, not a reason to put deployment or database
models back in Core. A JSON string already supplied as a flat value remains
verbatim; that is not the same as a typed nested JSON API.

See [the boundary and migration decision](core-cli-boundary.md).
