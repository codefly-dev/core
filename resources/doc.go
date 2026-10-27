// Package resources defines the codefly resource model — the typed
// hierarchy that every other package operates on.
//
// The resource hierarchy is:
//
//	Workspace → Module → Service → Endpoint
//	   │           │         │         │
//	   │           │         │         └── API kind (gRPC, REST, HTTP, TCP, …)
//	   │           │         └── Managed by an Agent
//	   │           └── Logical grouping of services
//	   └── Root (one per project)
//
// Each level is backed by a YAML file (workspace.codefly.yaml,
// module.codefly.yaml, service.codefly.yaml) and validated against a
// proto schema (see core/generated/go/codefly/base/v0). Loaders in
// this package read the YAML, populate the Go struct, and verify the
// proto contract.
//
// External consumers typically interact with this package via:
//
//   - LoadWorkspaceFromDir / FindWorkspaceUp — pick up the active workspace.
//   - Workspace.LoadModules / LoadServices — walk the hierarchy.
//   - Service.LoadEndpoints + FindGRPCEndpoint / FindHTTPEndpoint — endpoint discovery.
//   - Environment + Workspace.FindEnvironment — declared deploy targets.
//   - PlanReadiness / PlanEndpointProbes — declared health predicates (see docs/readiness.md).
//
// Workspace loading may migrate legacy flat-layout module declarations on disk.
// Inspection hosts use WithWorkspaceMigrationRefusal to refuse that migration
// throughout the import closure, and supply a resolver with appropriate effects.
// CreateWorkspace, AddModuleReference and Save are explicit write paths.
package resources
