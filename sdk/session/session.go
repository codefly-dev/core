// Package session defines the invocation identity and control-channel
// ownership contract shared by the Codefly SDK, which starts a dependency
// flow, and the Codefly CLI, which serves it.
//
// A default dependency session is disposable: it owns exactly one child CLI,
// and nothing else may share its control channel, its state directories or
// its containers. Identity is therefore minted from crypto/rand once per
// invocation. A caller-supplied naming scope is a human label; it is never
// the uniqueness primitive.
//
// Ownership mechanism: the SDK creates a private 0700 directory and asks the
// child to bind its gRPC control server on a Unix socket inside it. Only the
// SDK process can create that socket, so there is no bind race to lose and no
// deterministic workspace port for an unrelated server to occupy. The
// handshake then proves that the server answering on the socket holds the
// per-invocation secret, so a stale or foreign server is rejected before any
// environment read or destructive RPC.
//
// Platform support: Unix domain sockets, and therefore isolated sessions, are
// available on Linux and macOS. Windows callers must select the shared
// control channel explicitly.
package session

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// IDEnvironment carries the invocation identity of the dependency session.
	IDEnvironment = "CODEFLY_SESSION_ID"
	// SecretEnvironment carries the per-invocation handshake secret. It travels
	// only in the private child environment: never in command arguments, logs
	// or persisted workspace state.
	SecretEnvironment = "CODEFLY_SESSION_SECRET"
	// SocketEnvironment carries the absolute path of the Unix socket the child
	// must bind for its control server, inside a directory the SDK owns.
	SocketEnvironment = "CODEFLY_CLI_SERVER_SOCKET"
	// PortEnvironment is the legacy control-port override. Isolated sessions
	// strip it from the child environment so an inherited value cannot pull the
	// child back onto a shared workspace port.
	PortEnvironment = "CODEFLY_CLI_SERVER_PORT"
)

// ProtocolVersion is the version of the handshake contract this build speaks.
const ProtocolVersion = 1

// IsolatedControlSocketCapability is advertised by a control server that bound
// the SDK-owned socket instead of a deterministic workspace port.
const IsolatedControlSocketCapability = "isolated-control-socket"

// socketPathLimit is the smallest sun_path budget across supported platforms
// (104 bytes on macOS), minus room for the terminating NUL. Exceeding it fails
// at bind time with an opaque error, so the SDK checks the path up front.
const socketPathLimit = 100

// livenessProbeTimeout bounds the connect used to decide whether a leftover
// socket still has a server behind it. Loopback IPC either answers or refuses
// immediately; a longer wait would only stall a start that is going to fail.
const livenessProbeTimeout = 250 * time.Millisecond

// Session is the immutable identity of one dependency invocation.
type Session struct {
	ID     string
	Secret string
}

// New mints a fresh invocation identity.
func New() (*Session, error) {
	id, err := randomToken(16)
	if err != nil {
		return nil, fmt.Errorf("mint session identity: %w", err)
	}
	secret, err := randomToken(32)
	if err != nil {
		return nil, fmt.Errorf("mint session secret: %w", err)
	}
	return &Session{ID: id, Secret: secret}, nil
}

// FromEnvironment reads the session a parent SDK installed for this process.
// Control servers use it to answer the handshake. It returns nil when the
// process was not started as an isolated dependency session.
func FromEnvironment(environment []string) *Session {
	s := &Session{}
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		switch name {
		case IDEnvironment:
			s.ID = value
		case SecretEnvironment:
			s.Secret = value
		}
	}
	if s.ID == "" || s.Secret == "" {
		return nil
	}
	return s
}

// Inject returns environment with this session and its control socket
// installed, replacing any inherited value for the keys it owns.
func (s *Session) Inject(environment []string, socket string) []string {
	owned := map[string]string{
		IDEnvironment:     s.ID,
		SecretEnvironment: s.Secret,
		SocketEnvironment: socket,
	}
	result := make([]string, 0, len(environment)+len(owned))
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			result = append(result, entry)
			continue
		}
		if _, isOwned := owned[name]; isOwned || name == PortEnvironment {
			continue
		}
		result = append(result, entry)
	}
	for _, name := range []string{IDEnvironment, SecretEnvironment, SocketEnvironment} {
		result = append(result, name+"="+owned[name])
	}
	return result
}

// Scope is the backend-visible label for this invocation: short, lowercase and
// DNS-safe so it can be embedded in container names, state directory names and
// log roots. The full identity stays in the session metadata.
func (s *Session) Scope() string {
	return "s" + s.ID[:12]
}

// Challenge mints a nonce for one handshake. A fresh challenge per handshake
// means a recorded proof cannot be replayed by a later foreign server.
func Challenge() (string, error) {
	return randomToken(16)
}

// Proof signs a challenge with the session secret.
func (s *Session) Proof(challenge string) string {
	mac := hmac.New(sha256.New, []byte(s.Secret))
	fmt.Fprintf(mac, "codefly-session-v%d\x00%s\x00%s", ProtocolVersion, s.ID, challenge)
	return base64.RawStdEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyProof reports whether proof was produced by a peer holding this
// session's secret for exactly this challenge.
func (s *Session) VerifyProof(challenge string, proof string) bool {
	return hmac.Equal([]byte(s.Proof(challenge)), []byte(proof))
}

// Control is a private directory owned by the SDK process, holding the control
// socket of one dependency session and, for reusable sessions, its receipt.
type Control struct {
	Directory string
	Socket    string
}

// NewControl creates a private directory for one disposable session. The
// directory is fresh and unguessable, so two invocations — in the same
// package, in independent processes, or in two worktrees sharing a workspace
// name — can never select the same control channel.
func NewControl() (*Control, error) {
	if err := requireSocketSupport(); err != nil {
		return nil, err
	}
	root, err := socketRoot()
	if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(root, fmt.Sprintf("codefly-cli-%d-", os.Getpid()))
	if err != nil {
		return nil, fmt.Errorf("create private control directory: %w", err)
	}
	control, err := controlIn(directory)
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	return control, nil
}

// OpenControl returns the stable control directory for a reusable session,
// keyed by the caller's reuse fingerprint.
//
// Unlike a disposable session's directory, this name is predictable: the key
// is a hash of the workspace, service and run options, all of which any local
// user can derive from a checkout. It therefore lives under the caller's own
// cache root, where no one else can pre-create the path. In a world-writable
// root an attacker could plant a symlink there and capture the receipt — which
// carries the session secret — into a directory of their choosing.
func OpenControl(key string) (*Control, error) {
	if err := requireSocketSupport(); err != nil {
		return nil, err
	}
	root, err := warmRoot()
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(root, "codefly-warm-"+key)
	if err := os.Mkdir(directory, 0o700); err != nil {
		if !os.IsExist(err) {
			return nil, fmt.Errorf("create reusable control directory: %w", err)
		}
		// Lstat, not Stat: it reports the symlink itself rather than following
		// it, so a link planted at the path is refused instead of silently
		// redirecting the receipt. Chmod then fails for a directory owned by
		// anyone else.
		info, statErr := os.Lstat(directory)
		if statErr != nil {
			return nil, fmt.Errorf("inspect reusable control directory %s: %w", directory, statErr)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("reusable control path %s is not a directory", directory)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return nil, fmt.Errorf("take ownership of reusable control directory %s: %w", directory, err)
		}
	}
	return controlIn(directory)
}

func controlIn(directory string) (*Control, error) {
	socket := filepath.Join(directory, "c.sock")
	if len(socket) > socketPathLimit {
		return nil, fmt.Errorf("control socket path %s exceeds the %d byte platform limit: set TMPDIR to a shorter directory", socket, socketPathLimit)
	}
	return &Control{Directory: directory, Socket: socket}, nil
}

// Target is the gRPC dial target for this control socket.
func (c *Control) Target() string {
	return "unix:" + c.Socket
}

// Remove deletes the control directory and the socket inside it.
func (c *Control) Remove() error {
	return os.RemoveAll(c.Directory)
}

// ClearStaleSocket removes a leftover socket so a new child can bind the path,
// and refuses when a server is still listening on it.
//
// Unlinking a live socket does not disturb the server holding it — it keeps
// serving the connections it already has — but it frees the path, so a second
// child binds it and a second stack runs alongside the first over the same
// containers and state. Refusing turns that silent double-start back into the
// explicit failure the kernel used to give when two servers raced for one
// address. Callers must treat the error as fatal, not as a reason to spawn.
func (c *Control) ClearStaleSocket() error {
	info, err := os.Lstat(c.Socket)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect control socket %s: %w", c.Socket, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("control path %s already exists and is not a socket", c.Socket)
	}
	if conn, dialErr := net.DialTimeout("unix", c.Socket, livenessProbeTimeout); dialErr == nil {
		_ = conn.Close()
		return fmt.Errorf("a live control server already owns %s: refusing to displace it", c.Socket)
	}
	return os.Remove(c.Socket)
}

// Receipt records which session owns a reusable control directory and the
// fingerprint it was started with, so a later invocation only attaches to a
// stack built from a compatible plan.
type Receipt struct {
	ID          string `json:"id"`
	Secret      string `json:"secret"`
	Fingerprint string `json:"fingerprint"`
}

func (c *Control) receiptPath() string {
	return filepath.Join(c.Directory, "receipt.json")
}

// WriteReceipt records the owning session. The secret makes the file
// credential-bearing, so it is written 0600 inside the 0700 directory.
func (c *Control) WriteReceipt(receipt Receipt) error {
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode session receipt: %w", err)
	}
	return os.WriteFile(c.receiptPath(), encoded, 0o600)
}

// ReadReceipt returns the receipt left by the session that owns this control
// directory, or nil when there is none.
func (c *Control) ReadReceipt() (*Receipt, error) {
	content, err := os.ReadFile(c.receiptPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read session receipt: %w", err)
	}
	receipt := &Receipt{}
	if err := json.Unmarshal(content, receipt); err != nil {
		return nil, fmt.Errorf("decode session receipt: %w", err)
	}
	return receipt, nil
}

// RemoveReceipt drops the ownership record without touching the directory.
func (c *Control) RemoveReceipt() error {
	if err := os.Remove(c.receiptPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func requireSocketSupport() error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("isolated dependency sessions require Unix domain sockets, which Codefly does not support on Windows: select the shared control channel explicitly")
	}
	return nil
}

// warmRoot is the caller's own cache directory. A reusable control directory
// has a predictable name, so it must sit under a root only this user can write
// — see OpenControl.
func warmRoot() (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate the user cache directory for reusable sessions: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create the user cache directory %s: %w", root, err)
	}
	return root, nil
}

// socketRoot prefers the per-user temporary directory and falls back to /tmp
// when its path would push the socket past the platform's sun_path budget —
// macOS per-user TMPDIR paths are long enough for that to matter. Only
// disposable sessions use it: their directory name comes from MkdirTemp, so it
// is unguessable even in a world-writable root.
func socketRoot() (string, error) {
	candidates := []string{os.TempDir(), "/tmp"}
	// codefly-cli-<pid>-<10 random>/c.sock
	const reserved = len("codefly-cli-") + 12 + 10 + len("/c.sock") + 1
	for _, candidate := range candidates {
		if len(candidate)+reserved <= socketPathLimit {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no temporary directory short enough for a control socket: set TMPDIR to a shorter directory")
}

func randomToken(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
