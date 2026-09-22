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

`EnvironmentVariableManager.Configurations` never emits secret documents;
`Secrets` returns them separately with error propagation. Raw configuration
injection refuses structured data because it has no document identity. Promotable
GitOps rendering refuses resolved structured secrets, including configurations
containing no flat secret keys. It must carry declared external references.

SDK-Go exposes `ConfigurationDocument`, `SecretDocument` and workspace equivalents
plus typed decoding methods. This transport is generic runtime behavior, not a
reason to restore deployment or database models to Core. Existing flat JSON strings
remain strings and are not silently converted into documents.

See [the boundary and migration decision](core-cli-boundary.md).
