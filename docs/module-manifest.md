# The module manifest

The module manifest — the signed record of a module instance on a host, its
canonical bytes, its admission rules and its frontends — is part of the host's
delivery protocol, so it is defined beside the host rather than in core: in the
authority's `libraries/solutionhost/go` (`solutionhost`, with the workload
identity rule in its `workloadidentity` package), whose `docs/module-manifest.md`
is this page's successor.

Core keeps what the manifest is built from: the module contract
([module-contract.md](module-contract.md)), composition and resources.

## The document

The schema, `codefly/module-manifest/v1`, and its strict reader live with the
host library named above.
