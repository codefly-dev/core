package headers

const (
	UserID            = "X-Codefly-User-ID"
	UserAuthID        = "X-Codefly-User-Auth-ID"
	UserEmail         = "X-Codefly-User-Email"
	UserVerifiedEmail = "X-Codefly-User-Verified-Email"
	UserName          = "X-Codefly-User-Name"
	UserGivenName     = "X-Codefly-User-Given-Name"
	UserFamilyName    = "X-Codefly-User-Family-Name"
	UserPicture       = "X-Codefly-User-Picture"
	UserNickname      = "X-Codefly-User-Nickname"
	UserProfile       = "X-Codefly-User-Profile"
	UserLocale        = "X-Codefly-User-Locale"
	UserZoneInfo      = "X-Codefly-User-Zone-Information"
)

func UserHeaders() []string {
	return []string{
		UserID,
		UserAuthID,
		UserEmail,
		UserVerifiedEmail,
		UserName,
		UserGivenName,
		UserFamilyName,
		UserPicture,
		UserNickname,
		UserProfile,
		UserLocale,
		UserZoneInfo,
	}
}
