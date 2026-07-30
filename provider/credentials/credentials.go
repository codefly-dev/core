// Package credentials mints and resolves opaque credential handles as
// attenuated capabilities. A provider never receives a raw secret: it receives
// a handle bound to an exact principal, binding, action, request, origin,
// purpose, and injection location. The host resolves the handle only after
// every request check has already passed, injects the value without logging it,
// never returns it, and rejects any cross-purpose, cross-binding, or
// cross-action reuse. A handle is not permission; it is one narrow capability
// the host still re-checks at use.
package credentials

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/network/urlguard"
)

// InjectionKind is the closed set of host-owned injection locations.
type InjectionKind string

const (
	// InjectBearer sets Authorization: Bearer <secret>.
	InjectBearer InjectionKind = "bearer"
	// InjectHeader sets a named API-key header to <secret>.
	InjectHeader InjectionKind = "header"
	// InjectQuery sets a named query parameter to <secret>.
	InjectQuery InjectionKind = "query"
)

// Injection is where the host places a resolved secret. The provider never
// selects this; it is fixed at mint time from the plan.
type Injection struct {
	Kind InjectionKind
	Name string
}

func (i Injection) validate() error {
	switch i.Kind {
	case InjectBearer:
		if i.Name != "" {
			return fmt.Errorf("bearer injection takes no name")
		}
	case InjectHeader, InjectQuery:
		if i.Name == "" {
			return fmt.Errorf("%s injection requires a name", i.Kind)
		}
	default:
		return fmt.Errorf("unknown injection kind %q", i.Kind)
	}
	return nil
}

// Scope is the exact capability a handle grants. Every field is host-derived
// and immutable after minting.
type Scope struct {
	Principal      string
	Organization   string
	ArtifactDigest string
	Binding        *providerv0.BindingAddress
	PlanID         string
	ActionID       string
	RequestDigest  string
	Purpose        providerv0.CredentialPurpose
	Origin         urlguard.Origin
	Method         providerv0.HTTPMethod
	Injection      Injection
	MaxUses        uint32
	TTL            time.Duration
}

func (s Scope) validate() error {
	switch {
	case s.Principal == "" || s.Organization == "" || s.ArtifactDigest == "":
		return fmt.Errorf("scope requires principal, organization, and artifact digest")
	case s.Binding == nil || s.Binding.GetBindingId() == "":
		return fmt.Errorf("scope requires a binding")
	case s.PlanID == "" || s.ActionID == "" || s.RequestDigest == "":
		return fmt.Errorf("scope requires plan, action, and request identity")
	case s.Origin.Host == "":
		return fmt.Errorf("scope requires an admitted origin")
	case s.MaxUses == 0:
		return fmt.Errorf("scope requires at least one use")
	case s.TTL <= 0:
		return fmt.Errorf("scope requires a positive TTL")
	}
	switch s.Purpose {
	case providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT,
		providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME,
		providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_BUILD,
		providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_WEBHOOK_VERIFICATION:
	default:
		return fmt.Errorf("scope has an unknown credential purpose")
	}
	return s.Injection.validate()
}

// Use is the exact request context presented at resolution. It must match the
// minted scope in every dimension or resolution fails.
type Use struct {
	Purpose       providerv0.CredentialPurpose
	Binding       *providerv0.BindingAddress
	ActionID      string
	RequestDigest string
	Origin        urlguard.Origin
	Method        providerv0.HTTPMethod
}

type entry struct {
	secret    string
	scope     Scope
	expiresAt time.Time
	remaining uint32
}

// Vault mints and resolves handles. It holds raw secrets and never exposes
// them; only opaque handle IDs and safe correlation are observable.
type Vault struct {
	now     func() time.Time
	mu      sync.Mutex
	entries map[string]*entry
}

// NewVault builds an empty vault using the wall clock.
func NewVault() *Vault {
	return &Vault{now: time.Now, entries: make(map[string]*entry)}
}

// WithClock overrides the clock for deterministic tests.
func (v *Vault) WithClock(now func() time.Time) *Vault {
	v.now = now
	return v
}

// Mint stores secret under a fresh opaque handle bound to scope. The returned
// CredentialHandle carries only the opaque id and the purpose.
func (v *Vault) Mint(secret string, scope Scope) (*providerv0.CredentialHandle, error) {
	if secret == "" {
		return nil, fmt.Errorf("credential secret is required")
	}
	if err := scope.validate(); err != nil {
		return nil, err
	}
	id, err := newHandleID()
	if err != nil {
		return nil, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.entries[id] = &entry{
		secret:    secret,
		scope:     scope,
		expiresAt: v.now().Add(scope.TTL),
		remaining: scope.MaxUses,
	}
	return &providerv0.CredentialHandle{Handle: id, Purpose: scope.Purpose}, nil
}

// Inject resolves the handle against the exact use, decrements its remaining
// uses, and writes the secret into the request. It returns a scope error
// without ever echoing the secret. The request is mutated only on success.
func (v *Vault) Inject(request *http.Request, handle string, use Use) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	stored, ok := v.entries[handle]
	if !ok {
		return fmt.Errorf("credential handle is unknown")
	}
	if v.now().After(stored.expiresAt) {
		delete(v.entries, handle)
		return fmt.Errorf("credential handle has expired")
	}
	if stored.remaining == 0 {
		return fmt.Errorf("credential handle has no remaining uses")
	}
	if err := matchScope(stored.scope, use); err != nil {
		return err
	}
	apply(request, stored.scope.Injection, stored.secret)
	stored.remaining--
	if stored.remaining == 0 {
		delete(v.entries, handle)
	}
	return nil
}

func matchScope(scope Scope, use Use) error {
	if scope.Purpose != use.Purpose {
		return fmt.Errorf("credential purpose does not match handle")
	}
	if !sameBinding(scope.Binding, use.Binding) {
		return fmt.Errorf("credential binding does not match handle")
	}
	if scope.ActionID != use.ActionID {
		return fmt.Errorf("credential action does not match handle")
	}
	if scope.RequestDigest != use.RequestDigest {
		return fmt.Errorf("credential request does not match handle")
	}
	if scope.Origin != use.Origin {
		return fmt.Errorf("credential origin does not match handle")
	}
	if scope.Method != use.Method {
		return fmt.Errorf("credential method does not match handle")
	}
	return nil
}

func sameBinding(a, b *providerv0.BindingAddress) bool {
	return a.GetWorkspaceId() == b.GetWorkspaceId() &&
		a.GetEnvironmentId() == b.GetEnvironmentId() &&
		a.GetBindingId() == b.GetBindingId()
}

func apply(request *http.Request, injection Injection, secret string) {
	switch injection.Kind {
	case InjectBearer:
		request.Header.Set("Authorization", "Bearer "+secret)
	case InjectHeader:
		request.Header.Set(injection.Name, secret)
	case InjectQuery:
		query := request.URL.Query()
		query.Set(injection.Name, secret)
		request.URL.RawQuery = query.Encode()
	}
}

func newHandleID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("mint credential handle: %w", err)
	}
	return "cfh_" + hex.EncodeToString(buffer), nil
}
