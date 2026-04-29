// Package configurations routes service configurations and DNS
// declarations between codefly services.
//
// A "configuration" is a typed key/value bundle a service publishes
// for its dependents to consume — postgres exposes a connection
// string, vault exposes a token, an auth service exposes a JWKS URL.
// The Manager type aggregates configurations from every service in
// dependency order and injects them into dependent services as
// environment variables (CODEFLY__SERVICE_<NAME>__...).
//
// DNS declarations live alongside configurations: a service can ship
// a dns/<env>/dns.codefly.yaml to override its in-cluster Service
// hostname per deploy environment (used historically for
// host.docker.internal in local-mode `codefly run`). The Manager
// exposes those via GetDNS for the network package's RemoteManager
// to consume; missing DNS is now a non-fatal — the network layer
// synthesizes <svc>.<ns>.svc.cluster.local in cluster envs.
//
// Loader is the abstract interface a service must implement to plug
// into the manager (Identity, Load, Configurations, DNS). Concrete
// loaders live in core/cli and similar consumer packages.
package configurations
