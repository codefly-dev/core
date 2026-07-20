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
//
// # Secrets
//
// Files named *.secret.ref.env and *.secret.ref.yaml are reference-only secret
// manifests. Every value, including every scalar nested in YAML maps and
// arrays, must be a recognized provider reference:
//
//	# auth.secret.ref.env
//	CLIENT_SECRET=op://development-vault/auth-service/client-secret
//
//	# database.secret.ref.yaml
//	credentials:
//	  password: op://development-vault/database/password
//
// Backends are selected per environment via workspace.codefly.yaml. It is a
// list so more backends can be added later:
//
//	environments:
//	  - name: local
//	    secrets:
//	      - kind: 1password
//	        account: my-team
//
// Reference-only manifests are safe to commit by construction: plaintext,
// unknown schemes, malformed references, duplicate legacy/reference sources,
// and references without a configured backend all fail the load. Provider
// output is resolved in memory and is never written back to the manifest or
// included in provider failure output. A subsequent load resolves the stable
// reference again, so provider-side rotation requires no Git change.
//
// 1Password is the only backend today; SecretResolver is the seam for
// adding AWS Secrets Manager, Doppler, Vault, etc.
//
// Legacy *.secret.env and *.secret.yaml files remain a local plaintext escape
// hatch. They are ambiguous by design, must stay ignored by Git, and must not
// be copied or symlinked between worktrees. A locked, unavailable, or
// misconfigured provider fails closed; Codefly does not fall back to raw
// environment variables or values from a worktree manager.
//
// Git owns the non-secret manifest and workspace configuration, Core validates
// and resolves only provider references, the CLI selects the declared
// environment, and applications consume only the configuration Codefly routes
// to them. Worktree managers may run readiness checks but do not provision
// plaintext. See secrets.go (SecretResolver, ParseSecretReference,
// ResolversFromEnvironment).
package configurations
