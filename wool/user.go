package wool

// User identity context keys for propagation across service boundaries.
const (
	UserAuthIDKey    ContextKey = "codefly.user.auth.id"
	UserEmailKey     ContextKey = "codefly.user.email"
	UserNameKey      ContextKey = "codefly.user.name"
	UserGivenNameKey ContextKey = "codefly.user.given_name"
)

// ContextKeys lists all user identity keys for iteration.
var ContextKeys []ContextKey

func init() {
	ContextKeys = []ContextKey{
		UserAuthIDKey,
		UserEmailKey,
		UserNameKey,
		UserGivenNameKey,
	}
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

func (w *Wool) UserGivenName() (string, bool) {
	return w.lookup(UserGivenNameKey)
}
