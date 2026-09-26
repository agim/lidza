package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/agim/lidza/pkg/router"
)

// Account is a user as the auth pack knows it: the subject the app
// passed to Login, a label (the email claim, when the app sets one),
// when it was first and last seen, and whether an admin disabled it.
type Account struct {
	Subject     string     `json:"subject"`
	Label       string     `json:"label,omitempty"`
	DisabledAt  *time.Time `json:"disabledAt,omitempty"`
	FirstSeenAt time.Time  `json:"firstSeenAt"`
	LastSeenAt  time.Time  `json:"lastSeenAt"`
	// Sessions counts the open sessions.
	Sessions int `json:"sessions"`
}

// Disabled reports whether an admin disabled the account.
func (a Account) Disabled() bool { return a.DisabledAt != nil }

// Session is one refresh session of an account.
type Session struct {
	ID        string     `json:"id"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt time.Time  `json:"expiresAt"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
	RotatedAt *time.Time `json:"rotatedAt,omitempty"`
}

// ErrDisabled is the error a disabled account gets at login and refresh.
var ErrDisabled = router.Errorf(http.StatusForbidden, "account disabled")

// seen records a sign-in: the account row is created or its last-seen
// time and label updated. A disabled account is refused.
func (a *Auth) seen(ctx context.Context, subject string, claims map[string]any) error {
	label := ""
	if email, ok := claims["email"].(string); ok {
		label = email
	}
	var disabled *time.Time
	err := a.pool.QueryRow(ctx, `INSERT INTO auth_account (subject, label) VALUES ($1, NULLIF($2, ''))
		ON CONFLICT (subject) DO UPDATE SET last_seen_at = now(), label = COALESCE(NULLIF(EXCLUDED.label, ''), auth_account.label)
		RETURNING disabled_at`, subject, label).Scan(&disabled)
	if err != nil {
		return fmt.Errorf("auth: account: %w", err)
	}
	if disabled != nil {
		return ErrDisabled
	}
	return nil
}

// Accounts lists the accounts whose label or subject contains q (every
// account when q is empty), most recently seen first, with the total.
func (a *Auth) Accounts(ctx context.Context, q string, limit, offset int) ([]Account, int, error) {
	if limit <= 0 {
		limit = 50
	}
	pattern := "%" + strings.ToLower(strings.TrimSpace(q)) + "%"
	rows, err := a.pool.Query(ctx, `SELECT a.subject, coalesce(a.label, ''), a.disabled_at, a.first_seen_at, a.last_seen_at,
		(SELECT count(*) FROM auth_session s WHERE s.subject = a.subject AND s.revoked_at IS NULL AND s.expires_at > now())
		FROM auth_account a WHERE lower(coalesce(a.label, '')) LIKE $1 OR lower(a.subject) LIKE $1
		ORDER BY a.last_seen_at DESC LIMIT $2 OFFSET $3`, pattern, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var ac Account
		if err := rows.Scan(&ac.Subject, &ac.Label, &ac.DisabledAt, &ac.FirstSeenAt, &ac.LastSeenAt, &ac.Sessions); err != nil {
			return nil, 0, err
		}
		out = append(out, ac)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int
	if err := a.pool.QueryRow(ctx, `SELECT count(*) FROM auth_account WHERE lower(coalesce(label, '')) LIKE $1 OR lower(subject) LIKE $1`, pattern).Scan(&total); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// AccountOf returns one account; pgx.ErrNoRows when unknown.
func (a *Auth) AccountOf(ctx context.Context, subject string) (Account, error) {
	var ac Account
	err := a.pool.QueryRow(ctx, `SELECT a.subject, coalesce(a.label, ''), a.disabled_at, a.first_seen_at, a.last_seen_at,
		(SELECT count(*) FROM auth_session s WHERE s.subject = a.subject AND s.revoked_at IS NULL AND s.expires_at > now())
		FROM auth_account a WHERE a.subject = $1`, subject).Scan(&ac.Subject, &ac.Label, &ac.DisabledAt, &ac.FirstSeenAt, &ac.LastSeenAt, &ac.Sessions)
	return ac, err
}

// Disable blocks the account: its sessions end now and it cannot sign
// in or refresh until Enable.
func (a *Auth) Disable(ctx context.Context, subject string) error {
	tag, err := a.pool.Exec(ctx, `UPDATE auth_account SET disabled_at = now() WHERE subject = $1 AND disabled_at IS NULL`, subject)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, err := a.AccountOf(ctx, subject); errors.Is(err, pgx.ErrNoRows) {
			return router.NotFound("account")
		}
	}
	return a.RevokeAll(ctx, subject)
}

// Enable lifts Disable.
func (a *Auth) Enable(ctx context.Context, subject string) error {
	_, err := a.pool.Exec(ctx, `UPDATE auth_account SET disabled_at = NULL WHERE subject = $1`, subject)
	return err
}

// Sessions lists an account's sessions, newest first.
func (a *Auth) Sessions(ctx context.Context, subject string) ([]Session, error) {
	rows, err := a.pool.Query(ctx, `SELECT id, created_at, expires_at, revoked_at, rotated_at FROM auth_session WHERE subject = $1 ORDER BY created_at DESC LIMIT 100`, subject)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.CreatedAt, &s.ExpiresAt, &s.RevokedAt, &s.RotatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
