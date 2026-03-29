package wool

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

func (w *Wool) UserID() (string, bool) {
	return w.lookup(UserIDKey)
}

func (w *Wool) WithUserID(id string) {
	w.with(UserIDKey, id)
}

func (w *Wool) OrgID() (string, bool) {
	return w.lookup(OrgIDKey)
}

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
