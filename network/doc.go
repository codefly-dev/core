// Package network manages port allocation, DNS resolution, and
// kubernetes / container network instance creation for codefly
// services.
//
// Two managers cover the two execution contexts:
//
//   - RuntimeManager — local execution. Allocates deterministic
//     ports via ToNamedPort (a stable hash of workspace + module +
//     service + endpoint) so a developer's pgAdmin/DataGrip/browser
//     bookmark survives a `codefly run` restart.
//
//   - RemoteManager — k8s deploy. Generates network mappings backed
//     by cluster-internal Service DNS (<svc>.<ns>.svc.cluster.local)
//     when the workspace doesn't declare an explicit DNS override,
//     plus port-forward + log-fetch helpers for `codefly expose`.
//
// Three NetworkAccess types describe how a peer reaches an instance:
// Native (localhost), Container (host.docker.internal), and Public
// (load balancer / ingress). Callers filter mappings by access type
// to pick the right address for their context.
//
// DNS lookup is delegated to a DNSManager interface (see
// core/configurations.Manager) so the network layer doesn't depend on
// the YAML loaders directly — useful in tests.
package network
