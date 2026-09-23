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

This changes resource composition only. Deployment declarations and artifact
acquisition remain CLI responsibilities; no agent protocol changes are needed.
