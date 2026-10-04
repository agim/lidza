package auth

import (
	"context"
	"log/slog"
	"net/http"
)

// The account events the routes report to Options.OnEvent, for an app's
// security log (who signed in, from where, and what failed).
const (
	EventSignedIn            = "signed_in"             // a session opened: password, provider, or after a sign-up
	EventSignInFailed        = "sign_in_failed"        // Reason: bad_credentials, not_verified, refused, disabled, or a provider's (denied, state, provider, signup)
	EventSignedOut           = "signed_out"            // the logout route
	EventSignedUp            = "signed_up"             // a registration, or a provider's first sign-in that made the account
	EventPasswordChanged     = "password_changed"      // the signed-in user's change; every other session ended
	EventPasswordCheckFailed = "password_check_failed" // a wrong current password on the change or delete route
	EventResetRequested      = "reset_requested"       // a reset link sent (an unknown address reports nothing)
	EventPasswordReset       = "password_reset"        // a reset link used; every session ended
	EventEmailVerified       = "email_verified"        // a verification link used
	EventAccountDeleted      = "account_deleted"       // the delete route; Subject is gone by then
	EventConnected           = "connected"             // an external account connected (Method: the provider)
	EventConnectFailed       = "connect_failed"        // Reason: denied, state, scopes, refused, provider
	EventDisconnected        = "disconnected"          // a connection removed and revoked at the provider
)

// Event is one account event, as Options.OnEvent sees it.
type Event struct {
	Kind string
	// Subject is the account; empty for a failed sign-in with an address
	// no account has, and for a provider sign-in that failed before the
	// account was known.
	Subject string
	// Email is the address the request named or the account's.
	Email string
	// Method is "password", or the provider's name ("google").
	Method string
	// Reason says why a sign-in failed; empty otherwise.
	Reason string
	// Request is the request (the client address, the user agent).
	Request *http.Request
}

// event reports e to OnEvent; a panic there is logged, never the
// request's.
func (s *signin) event(ctx context.Context, r *http.Request, e Event) {
	if s.opt.OnEvent == nil {
		return
	}
	e.Request = r
	defer func() {
		if v := recover(); v != nil {
			slog.Error("auth: OnEvent panicked", "kind", e.Kind, "panic", v)
		}
	}()
	s.opt.OnEvent(ctx, e)
}

type eventRequestKey struct{}

// withEventRequest keeps r for the events reported deeper in (a sign-up).
func withEventRequest(ctx context.Context, r *http.Request) context.Context {
	return context.WithValue(ctx, eventRequestKey{}, r)
}

func eventRequest(ctx context.Context) *http.Request {
	r, _ := ctx.Value(eventRequestKey{}).(*http.Request)
	return r
}

// claimString is a string claim, or "".
func claimString(claims map[string]any, key string) string {
	v, _ := claims[key].(string)
	return v
}
