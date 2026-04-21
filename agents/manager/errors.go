package manager

import "errors"

// Sentinel errors for agent loading failures. Callers (CLI, MCP server,
// daemon) should switch on these via errors.Is to pick the right user
// message and remediation.
//
// Each is wrapped on the way out — the underlying error chain still
// carries the original cause + stderr tail for diagnostics. errors.Is
// matches through the chain.
var (
	// ErrAgentNil is returned by Load when called with a nil agent ref.
	// Programmer error — should never reach the user.
	ErrAgentNil = errors.New("agent: nil reference")

	// ErrAgentBinaryNotFound is returned when the agent binary cannot be
	// resolved via local cache, NixStore, OCIStore, or GitHub.
	// Remediation: `codefly agent build`, set AGENT_NIX_FLAKE /
	// AGENT_REGISTRY, or check publisher/name/version in the agent ref.
	ErrAgentBinaryNotFound = errors.New("agent: binary not found")

	// ErrAgentSpawn is returned when exec.Cmd.Start fails. Almost always
	// a permissions issue (binary not executable) or kernel exec failure.
	ErrAgentSpawn = errors.New("agent: spawn failed")

	// ErrAgentHandshakeTimeout is returned when the agent process did
	// not print its VERSION|PORT handshake within startupTimeout.
	// Remediation: check the agent's stderr (via AgentConn.StderrTail
	// after Close, or pass a logWriter) for the panic / startup error.
	ErrAgentHandshakeTimeout = errors.New("agent: handshake timeout")

	// ErrAgentHandshakeMalformed is returned when the agent emitted a
	// first stdout line that doesn't parse as VERSION|PORT.
	ErrAgentHandshakeMalformed = errors.New("agent: handshake malformed")

	// ErrAgentVersionMismatch fires when the agent advertises a protocol
	// version this loader doesn't speak. Remediation: rebuild agent
	// against current core, or upgrade core.
	ErrAgentVersionMismatch = errors.New("agent: protocol version mismatch")

	// ErrAgentDialTimeout is returned when the gRPC connection to the
	// spawned agent didn't reach Ready within dialTimeout. The agent is
	// listening on the announced port but isn't accepting connections —
	// usually a server misconfiguration or a TLS mismatch.
	ErrAgentDialTimeout = errors.New("agent: gRPC dial timeout")
)

// Sentinels for the AgentStore implementations.
var (
	// ErrStoreUnavailable is returned by Available when the configured
	// store cannot be reached (network down, registry refusing).
	ErrStoreUnavailable = errors.New("store: unavailable")

	// ErrStoreArtifactMissing is returned by Pull when the agent ref
	// resolves to nothing in the store. Distinct from ErrStoreUnavailable
	// to let callers retry-with-backoff vs fail-fast appropriately.
	ErrStoreArtifactMissing = errors.New("store: artifact missing")
)
