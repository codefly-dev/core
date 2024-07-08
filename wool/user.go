package wool

const (
	UserAuthIDKey    ContextKey = "codefly.user.auth.id"
	UserEmailKey     ContextKey = "codefly.user.email"
	UserNameKey      ContextKey = "codefly.user.name"
	UserGivenNameKey ContextKey = "codefly.user.given_name"
)

var ContextKeys []ContextKey

func init() {
	ContextKeys = []ContextKey{
		UserAuthIDKey,
		UserEmailKey,
		UserNameKey,
		UserGivenNameKey,
	}
}

// UserAuthID returns the ID from the Auth process from the context
func (w *Wool) UserAuthID() (string, bool) {
	return w.lookup(UserAuthIDKey)
}

func (w *Wool) WithUserAuthID(authID string) {
	w.with(UserAuthIDKey, authID)
}

// UserEmail returns the UserEmail from the context
func (w *Wool) UserEmail() (string, bool) {
	return w.lookup(UserEmailKey)
}

func (w *Wool) WithUserEmail(s string) {
	w.with(UserEmailKey, s)
}

// UserName returns the UserName from the context
func (w *Wool) UserName() (string, bool) {
	return w.lookup(UserNameKey)
}

// UserGivenName returns the UserGivenName from the context
func (w *Wool) UserGivenName() (string, bool) {
	return w.lookup(UserGivenNameKey)
}
