package wool

import "context"

// User identity context keys for propagation across service boundaries.
const (
	// Standard identity headers (set by auth sidecar)
	UserIDKey ContextKey = "user.id"
	OrgIDKey  ContextKey = "org.id"
	RolesKey  ContextKey = "user.roles"

	// Auth provider identity (from JWT/gateway)
	UserAuthIDKey ContextKey = "user.auth.id"
	UserEmailKey  ContextKey = "user.email"
	UserNameKey   ContextKey = "user.name"
)

// ContextKeys lists all user identity keys for iteration.
var ContextKeys []ContextKey

func init() {
	ContextKeys = []ContextKey{
		UserIDKey,
		OrgIDKey,
		RolesKey,
		UserAuthIDKey,
		UserEmailKey,
		UserNameKey,
	}
}

// WithUserID and WithOrgID bind identity onto a CONTEXT, and are the form that
// propagates.
//
// The methods below do not, and cannot: `with` assigns to the Wool's own ctx
// field, and Get builds a fresh Wool for every call, so the context a method
// derives is discarded the moment that Wool goes out of scope. Code that does
//
//	wool.Get(ctx).WithOrgID(id)          // no effect on ctx
//	id, ok := wool.Get(ctx).OrgID()      // ok is false
//
// reads back nothing, with no error and no log line to say why — which is how a
// consumer discovered an identity-defaulted read that had never once worked.
// Written as
//
//	ctx = wool.WithOrgID(ctx, id)
//
// it propagates to every Get on the returned context, which is what the
// accessors were always for.
//
// Both are no-ops for an empty value: a caller that has no organization should
// not plant an empty one for a later reader to treat as present.
func WithUserID(ctx context.Context, id string) context.Context {
	return withIdentity(ctx, UserIDKey, id)
}

func WithOrgID(ctx context.Context, id string) context.Context {
	return withIdentity(ctx, OrgIDKey, id)
}

func withIdentity(ctx context.Context, key ContextKey, value string) context.Context {
	if ctx == nil || value == "" {
		return ctx
	}
	return context.WithValue(ctx, key, value)
}

func (w *Wool) UserID() (string, bool) {
	return w.lookup(UserIDKey)
}

// WithUserID binds the id on THIS Wool only; it does not reach the context the
// Wool was built from. Use the package-level WithUserID to propagate.
//
// Deprecated: use wool.WithUserID(ctx, id), which returns the context.
func (w *Wool) WithUserID(id string) {
	w.with(UserIDKey, id)
}

func (w *Wool) OrgID() (string, bool) {
	return w.lookup(OrgIDKey)
}

// WithOrgID binds the id on THIS Wool only; it does not reach the context the
// Wool was built from. Use the package-level WithOrgID to propagate.
//
// Deprecated: use wool.WithOrgID(ctx, id), which returns the context.
func (w *Wool) WithOrgID(id string) {
	w.with(OrgIDKey, id)
}

func (w *Wool) Roles() (string, bool) {
	return w.lookup(RolesKey)
}

func (w *Wool) UserAuthID() (string, bool) {
	return w.lookup(UserAuthIDKey)
}

func (w *Wool) WithUserAuthID(authID string) {
	w.with(UserAuthIDKey, authID)
}

func (w *Wool) UserEmail() (string, bool) {
	return w.lookup(UserEmailKey)
}

func (w *Wool) WithUserEmail(s string) {
	w.with(UserEmailKey, s)
}

func (w *Wool) UserName() (string, bool) {
	return w.lookup(UserNameKey)
}
