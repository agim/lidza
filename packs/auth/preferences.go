package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/agim/lidza/pkg/router"
	"github.com/jackc/pgx/v5"
)

// Preference limits bound the complete account document, each value and its keys.
// Preferences are non-sensitive display choices, never credentials or permissions.
const (
	MaxPreferenceKeys       = 128
	MaxPreferenceBytes      = 32768
	MaxPreferenceValueBytes = 4096
)

var preferenceKey = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,95}$`)

// PreferenceValues is a namespaced map of JSON display choices. Apps own key
// names and validate their value shapes. A null in a patch removes that key.
type PreferenceValues map[string]json.RawMessage

// PreferenceState is the authenticated account's durable preference document.
type PreferenceState struct {
	Subject string           `json:"subject"`
	Values  PreferenceValues `json:"values"`
}

// PreferencePatch changes individual keys, preserving unrelated choices atomically.
// The owner always comes from CurrentUser, never from request data.
type PreferencePatch struct {
	// Account optionally checks which signed-in account the caller expects.
	// It cannot select an owner; a changed account is rejected before storage.
	Account string           `json:"account,omitempty"`
	Values  PreferenceValues `json:"values"`
}

func (p PreferencePatch) Validate() error {
	if p.Values == nil || len(p.Values) > MaxPreferenceKeys {
		return router.Errorf(http.StatusUnprocessableEntity, "preferences require a bounded values object")
	}
	for key, value := range p.Values {
		if !preferenceKey.MatchString(key) || len(value) > MaxPreferenceValueBytes || !json.Valid(value) {
			return router.Errorf(http.StatusUnprocessableEntity, "invalid preference key or value")
		}
	}
	raw, err := json.Marshal(p.Values)
	if err != nil || len(raw) > MaxPreferenceBytes {
		return router.Errorf(http.StatusUnprocessableEntity, "preferences exceed the account size limit")
	}
	return nil
}

func preferenceSubject(ctx context.Context) (string, error) {
	user := CurrentUser(ctx)
	if user == nil || user.ID == "" {
		return "", router.Errorf(http.StatusUnauthorized, "authentication required")
	}
	return user.ID, nil
}

// Preferences reads only the authenticated account's choices. They survive
// sessions and nodes. No preference is added to authorization/session claims.
func (a *Auth) Preferences(ctx context.Context) (PreferenceState, error) {
	subject, err := preferenceSubject(ctx)
	if err != nil {
		return PreferenceState{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var raw []byte
	err = a.pool.QueryRow(ctx, `SELECT coalesce(p.values, '{}'::jsonb)
		FROM auth_account a LEFT JOIN auth_preference p ON p.subject = a.subject
		WHERE a.subject = $1 AND a.disabled_at IS NULL`, subject).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return PreferenceState{}, router.Errorf(http.StatusUnauthorized, "account unavailable")
	}
	if err != nil {
		return PreferenceState{}, fmt.Errorf("auth: read preferences: %w", err)
	}
	var values PreferenceValues
	if err = json.Unmarshal(raw, &values); err != nil {
		return PreferenceState{}, errors.New("auth: invalid stored preferences")
	}
	if values == nil {
		values = PreferenceValues{}
	}
	return PreferenceState{Subject: subject, Values: values}, nil
}

// PatchPreferences serializes patches per account, so concurrent edits of
// different keys cannot lose each other. Validation failures roll back entirely.
func (a *Auth) PatchPreferences(ctx context.Context, patch PreferencePatch) (PreferenceState, error) {
	subject, err := preferenceSubject(ctx)
	if err != nil {
		return PreferenceState{}, err
	}
	if patch.Account != "" && patch.Account != subject {
		return PreferenceState{}, router.Errorf(http.StatusConflict, "signed-in account changed")
	}
	if err = patch.Validate(); err != nil {
		return PreferenceState{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return PreferenceState{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Coordinate with account deletion/disable, including a request whose
	// authentication completed immediately before deletion began.
	var account string
	err = tx.QueryRow(ctx, `SELECT subject FROM auth_account WHERE subject=$1 AND disabled_at IS NULL FOR SHARE`, subject).Scan(&account)
	if errors.Is(err, pgx.ErrNoRows) {
		return PreferenceState{}, router.Errorf(http.StatusUnauthorized, "account unavailable")
	}
	if err != nil {
		return PreferenceState{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO auth_preference(subject, values) VALUES ($1,'{}') ON CONFLICT(subject) DO NOTHING`, subject); err != nil {
		return PreferenceState{}, err
	}
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT values FROM auth_preference WHERE subject=$1 FOR UPDATE`, subject).Scan(&raw); err != nil {
		return PreferenceState{}, err
	}
	values := PreferenceValues{}
	if err = json.Unmarshal(raw, &values); err != nil {
		return PreferenceState{}, errors.New("auth: invalid stored preferences")
	}
	for key, value := range patch.Values {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			delete(values, key)
		} else {
			values[key] = value
		}
	}
	if err = (PreferencePatch{Values: values}).Validate(); err != nil {
		return PreferenceState{}, err
	}
	raw, err = json.Marshal(values)
	if err != nil {
		return PreferenceState{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE auth_preference SET values=$2,updated_at=now() WHERE subject=$1`, subject, raw); err != nil {
		return PreferenceState{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PreferenceState{}, err
	}
	return PreferenceState{Subject: subject, Values: values}, nil
}

func (s *signin) authPreferences(ctx context.Context, req *router.Request[router.None]) (PreferenceState, error) {
	req.Header().Set("Cache-Control", "private, no-store")
	return From(ctx).Preferences(ctx)
}

func (s *signin) authPatchPreferences(ctx context.Context, req *router.Request[PreferencePatch]) (PreferenceState, error) {
	req.Header().Set("Cache-Control", "private, no-store")
	return From(ctx).PatchPreferences(ctx, req.Body)
}

func preferenceRequestLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		r.Body = http.MaxBytesReader(w, r.Body, MaxPreferenceBytes+8192)
		next.ServeHTTP(w, r)
	})
}
