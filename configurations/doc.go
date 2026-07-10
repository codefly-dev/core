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
// Secret values (from *.secret.env / *.secret.yaml files) may be either
// literal plaintext or a reference the environment's secret backend
// resolves at Load() time:
//
//	client_secret: op://dev-vault/auth0/client_secret       # 1Password
//	client_secret: aws-sm://codefly/dev/auth0#client_secret # AWS Secrets Manager
//	client_secret: doppler://AUTH0_CLIENT_SECRET            # Doppler
//
// Backends are selected per environment via workspace.codefly.yaml. More
// than one can be listed so op:// and aws-sm:// references coexist:
//
//	environments:
//	  - name: local
//	    secrets:
//	      - kind: 1password
//	        account: my-team
//	  - name: production
//	    secrets:
//	      - kind: aws-secrets-manager
//	        region: us-east-1
//
// References are safe to commit and resolved in memory — the plaintext
// value never touches disk. A reference whose backend is not configured
// for the environment fails the load rather than leaking the raw URI.
//
// Plaintext secret values still work (the CONNECTION=postgres://… escape
// hatch), but they sit unencrypted on disk and are local/dev-only; a
// plaintext secret used against a configured backend in a non-local
// environment is logged as a warning. See secrets.go (SecretResolver,
// ParseSecretReference, ResolversFromEnvironment).
package configurations
