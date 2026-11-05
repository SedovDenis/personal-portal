package main

// portalNav describes the shared navigation, independent of the current module.
// Future services (such as Calendar) can reuse the same template and session.
type portalNav struct {
	UserName  string
	Username  string
	IsAdmin   bool
	CSRFToken string
	Active    string
}

func newPortalNav(user *portalUser, csrfToken, active string) portalNav {
	nav := portalNav{CSRFToken: csrfToken, Active: active}
	if user != nil {
		nav.UserName = user.DisplayName
		nav.Username = user.Username
		nav.IsAdmin = user.IsAdmin
	}
	return nav
}
