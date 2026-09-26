package main

import (
	"embed"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/admin"
	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/pkg/router"

	"notes/handlers"
)

// adminFiles holds the templates of the app's admin pages.
//
//go:embed admin/*.html
var adminFiles embed.FS

// routes registers the API. Every route lives under /api; the frontend
// never defines one. GET /api/v1/health is built in. router.Route gives a
// handler typed input and output: the client in .lidza/client is generated
// from these types, so the frontend cannot drift from the API.
func routes(r *router.Router) {
	// Public, and throttled per client address (AUTH_LOGIN_RPS): the
	// routes that take credentials or send emails.
	router.Route(r, "POST /api/v1/auth/register", handlers.Register, auth.Throttle())
	router.Route(r, "POST /api/v1/auth/login", handlers.Login, auth.Throttle())
	router.Route(r, "POST /api/v1/auth/verify", handlers.VerifyEmail, auth.Throttle())
	router.Route(r, "POST /api/v1/auth/forgot", handlers.ForgotPassword, auth.Throttle())
	router.Route(r, "POST /api/v1/auth/reset", handlers.ResetPassword, auth.Throttle())

	// Visitors and users alike: auth.Optional() fills in the user when the
	// request carries a valid token and continues without one otherwise.
	session := r.Group("/api/v1/auth/session", auth.Optional())
	router.Route(session, "GET /api/v1/auth/session", handlers.CurrentSession)

	// Signed in only: auth.Require() reads the access cookie set at login,
	// or an Authorization: Bearer header, and replies 401 otherwise. The
	// two public routes above are more specific than the group's prefix,
	// so they stay public.
	account := r.Group("/api/v1/auth", auth.Require())
	router.Route(account, "GET /api/v1/auth/me", handlers.Me)
	router.Route(account, "POST /api/v1/auth/logout", handlers.Logout)

	notes := r.Group("/api/v1/notes", auth.Require())
	handlers.NoteRoutes(notes)
	handlers.AttachmentRoutes(notes)

	// The admin pages at /admin, for the first account and the users
	// ADMIN_USERS names: users and sign-in providers, mail, the model and
	// storage with their settings, jobs. Themed by admin/theme.css.
	// handlers.AdminNotes adds the app's own page to them, rendered from
	// admin/notes.html, embedded so it ships in the binary.
	admin.Mount(r, admin.Options{Title: "notes", Templates: lidza.Sub(adminFiles, "admin"), Pages: []admin.Page{handlers.AdminNotes()}})
}
