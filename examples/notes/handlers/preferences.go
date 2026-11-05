package handlers

import (
	"context"

	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/pkg/router"
)

// Preferences reads the signed-in account's display choices (the order
// of the notes list), kept by the auth pack so they follow the account
// across browsers. An app on auth.Mount has these routes already; this
// app writes its own sign-in routes, so it mounts the pack's methods
// itself, with the pack's types: the client gets api.preferences and
// api.patchPreferences, and src/preferences.ts reads them.
func Preferences(ctx context.Context, req *router.Request[router.None]) (auth.PreferenceState, error) {
	req.Header().Set("Cache-Control", "private, no-store")
	return auth.From(ctx).Preferences(ctx)
}

// PatchPreferences changes some choices and leaves the others: a null
// removes a key. The pack validates the keys, each value's size and the
// whole document, and refuses a patch sent for another account (409).
func PatchPreferences(ctx context.Context, req *router.Request[auth.PreferencePatch]) (auth.PreferenceState, error) {
	req.Header().Set("Cache-Control", "private, no-store")
	return auth.From(ctx).PatchPreferences(ctx, req.Body)
}
