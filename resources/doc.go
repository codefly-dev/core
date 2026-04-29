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
//
// All loader functions are read-only; they don't mutate the on-disk
// YAML. Mutators (CreateWorkspace, AddModuleReference, Save) are the
// explicit write path.
package resources
