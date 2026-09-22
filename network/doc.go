// Package network manages runtime port allocation, DNS resolution, and
// native/container network instances for codefly services.
//
//   - RuntimeManager — local execution. Allocates deterministic
//     ports via ToNamedPort (a stable hash of workspace + module +
//     service + endpoint + runtime port mode) so a developer's
//     pgAdmin/DataGrip/browser bookmark survives a `codefly run`
//     restart, while the same service running under distinct runtimes
//     (native/nix vs container) gets non-colliding host ports.
//
// Kubernetes deployment DNS, port forwarding and log streaming belong to
// the CLI's remotenetwork package.
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
