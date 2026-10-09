package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The owner claim (AUTH_OWNER_CLAIM=true): on a deployment anyone can
// reach, the first account to sign in must not own the app just by being
// first. With the claim on, the app's first account (FirstSubject, an
// admin) is the account that presents a one-time token, delivered out of
// band:
//
//   - The token is AUTH_OWNER_CLAIM_TOKEN when a deployment platform
//     supplies one (several nodes share it), else generated once into
//     <AUTH_OWNER_CLAIM_DIR>/token (mode 0600, the directory 0700) for
//     the operator or the hosting agent to read. Only its SHA-256 is
//     stored, in the auth_owner_claim row. It is never logged or sent.
//   - <AUTH_OWNER_CLAIM_DIR>/status.json says {"state": "unclaimed"} or
//     {"state": "claimed", "claimedAt": ...}: the contract a hosting
//     agent reads. The token file exists only while unclaimed.
//   - A signed-in account claims with ClaimOwner (the route POST
//     /api/v1/auth/owner/claim, or the admin pack's form). The claim is
//     one conditional UPDATE: of two at once, one wins. A wrong token
//     changes nothing. On success the token file is removed for good.
//   - Ownership never reopens: deleting the owner or ending its sessions
//     keeps the row. `lidza auth owner rotate` replaces an unclaimed
//     token; a claimed app has no token to rotate. Recovery of a lost
//     owner is ADMIN_USERS in the credentials, as for any admin.
//   - Turning the claim on in an app that already has a first account
//     records that account as the owner: nothing reopens.

// OwnerClaimDir is where the token and status files live unless
// AUTH_OWNER_CLAIM_DIR says otherwise; config/ is the app's private
// directory (the master key's), and .gitignore keeps the claim out.
const OwnerClaimDir = "config/owner-claim"

// OwnerClaimTable is the DDL of the owner claim (model AuthOwnerClaim,
// table auth_owner_claim): one row, id "owner". Tests create it
// directly.
const OwnerClaimTable = `CREATE TABLE IF NOT EXISTS auth_owner_claim (
  id text PRIMARY KEY,
  token_hash text NOT NULL,
  subject text,
  claimed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);`

// Owner claim errors; the route answers 403 and 409.
var (
	ErrOwnerClaimOff     = errors.New("auth: the owner claim is off (AUTH_OWNER_CLAIM)")
	ErrOwnerClaimed      = errors.New("auth: the app already has its owner")
	ErrOwnerToken        = errors.New("auth: the owner claim token does not match")
	errOwnerTokenTooWeak = errors.New("AUTH_OWNER_CLAIM_TOKEN must be at least 32 characters")
)

// OwnerStatus is what status.json and OwnerClaimStatus report.
type OwnerStatus struct {
	// State is "unclaimed" or "claimed".
	State     string     `json:"state"`
	ClaimedAt *time.Time `json:"claimedAt,omitempty"`
}

func (a *Auth) claimDir() string {
	if a.cfg.OwnerClaimDir != "" {
		return a.cfg.OwnerClaimDir
	}
	return OwnerClaimDir
}

func hashOwnerToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// startOwnerClaim prepares the claim when AUTH_OWNER_CLAIM is on: records
// an existing first account as the owner, else makes sure a token's hash
// is stored and the token file (or the platform's token) is in place,
// and writes status.json.
func (a *Auth) startOwnerClaim(ctx context.Context) error {
	if !a.cfg.OwnerClaim {
		return nil
	}
	if t := a.cfg.OwnerClaimToken; t != "" && len(t) < 32 {
		return errOwnerTokenTooWeak
	}
	var subject *string
	var claimedAt *time.Time
	var stored string
	err := a.pool.QueryRow(ctx, `SELECT token_hash, subject, claimed_at FROM auth_owner_claim WHERE id = 'owner'`).Scan(&stored, &subject, &claimedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// An app that already has a first account keeps it as its owner.
		var first string
		qerr := a.pool.QueryRow(ctx, `SELECT subject FROM auth_session ORDER BY created_at, id LIMIT 1`).Scan(&first)
		if qerr != nil && !errors.Is(qerr, pgx.ErrNoRows) {
			return qerr
		}
		if first != "" {
			if _, err := a.pool.Exec(ctx, `INSERT INTO auth_owner_claim (id, token_hash, subject, claimed_at) VALUES ('owner', '', $1, now()) ON CONFLICT (id) DO NOTHING`, first); err != nil {
				return err
			}
			return a.startOwnerClaim(ctx)
		}
		token, err := a.claimToken(true)
		if err != nil {
			return err
		}
		if _, err := a.pool.Exec(ctx, `INSERT INTO auth_owner_claim (id, token_hash) VALUES ('owner', $1) ON CONFLICT (id) DO NOTHING`, hashOwnerToken(token)); err != nil {
			return err
		}
		return a.startOwnerClaim(ctx)
	case err != nil:
		return fmt.Errorf("auth: reading the owner claim (does auth_owner_claim exist? lidza gen, then lidza db migrate): %w", err)
	}
	if subject != nil {
		a.removeClaimToken()
		return a.writeOwnerStatus(OwnerStatus{State: "claimed", ClaimedAt: claimedAt})
	}
	token, err := a.claimToken(true)
	if err != nil {
		return err
	}
	if hashOwnerToken(token) != stored {
		if a.cfg.OwnerClaimToken != "" {
			// The platform's token is the one to use: a new one replaces
			// the stored hash while unclaimed.
			if _, err := a.pool.Exec(ctx, `UPDATE auth_owner_claim SET token_hash = $1, updated_at = now() WHERE id = 'owner' AND subject IS NULL`, hashOwnerToken(token)); err != nil {
				return err
			}
		} else {
			slog.Warn("auth: the owner claim token file does not match the stored claim (another node made it?): set AUTH_OWNER_CLAIM_TOKEN on every node, or run lidza auth owner rotate", "dir", a.claimDir())
		}
	}
	return a.writeOwnerStatus(OwnerStatus{State: "unclaimed"})
}

// claimToken is the platform's token, else the token file's, else (with
// create) a new one written to the token file.
func (a *Auth) claimToken(create bool) (string, error) {
	if a.cfg.OwnerClaimToken != "" {
		return a.cfg.OwnerClaimToken, nil
	}
	path := filepath.Join(a.claimDir(), "token")
	if data, err := os.ReadFile(path); err == nil {
		if t := strings.TrimSpace(string(data)); t != "" {
			return t, nil
		}
	}
	if !create {
		return "", os.ErrNotExist
	}
	return a.writeClaimToken(false)
}

// writeClaimToken makes a new token and writes it to the token file,
// replacing one there only when replace says so.
func (a *Auth) writeClaimToken(replace bool) (string, error) {
	if err := a.ensureClaimDir(); err != nil {
		return "", err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	path := filepath.Join(a.claimDir(), "token")
	flag := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if replace {
		tmp := path + ".new"
		if err := os.WriteFile(tmp, []byte(token+"\n"), 0o600); err != nil {
			return "", err
		}
		return token, os.Rename(tmp, path)
	}
	f, err := os.OpenFile(path, flag, 0o600)
	if errors.Is(err, os.ErrExist) {
		// Another process wrote it first: use that one.
		return a.claimToken(false)
	}
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(token + "\n"); err != nil {
		f.Close()
		return "", err
	}
	return token, f.Close()
}

func (a *Auth) removeClaimToken() {
	path := filepath.Join(a.claimDir(), "token")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Error("auth: removing the claimed owner token file", "path", path, "error", err)
	}
}

// ensureClaimDir makes the claim directory, private, with a .gitignore
// of its own so neither file is ever committed, wherever it is.
func (a *Auth) ensureClaimDir() error {
	if err := os.MkdirAll(a.claimDir(), 0o700); err != nil {
		return err
	}
	os.Chmod(a.claimDir(), 0o700)
	gi := filepath.Join(a.claimDir(), ".gitignore")
	if _, err := os.Stat(gi); err != nil {
		return os.WriteFile(gi, []byte("# The owner claim token and status: never committed.\n*\n"), 0o644)
	}
	return nil
}

func (a *Auth) writeOwnerStatus(st OwnerStatus) error {
	if err := a.ensureClaimDir(); err != nil {
		return err
	}
	data, _ := json.Marshal(st)
	path := filepath.Join(a.claimDir(), "status.json")
	tmp := path + ".new"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// OwnerClaimFile is where the token file is, for a page to tell the
// operator; "" when the deployment platform supplies the token
// (AUTH_OWNER_CLAIM_TOKEN) and holds it.
func (a *Auth) OwnerClaimFile() string {
	if a.cfg.OwnerClaimToken != "" {
		return ""
	}
	return filepath.Join(a.claimDir(), "token")
}

// OwnerClaimStatus reports whether the app is waiting for its owner; an
// app without the claim on reports ErrOwnerClaimOff.
func (a *Auth) OwnerClaimStatus(ctx context.Context) (OwnerStatus, error) {
	if !a.cfg.OwnerClaim {
		return OwnerStatus{}, ErrOwnerClaimOff
	}
	var subject *string
	var claimedAt *time.Time
	err := a.pool.QueryRow(ctx, `SELECT subject, claimed_at FROM auth_owner_claim WHERE id = 'owner'`).Scan(&subject, &claimedAt)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && subject == nil {
		return OwnerStatus{State: "unclaimed"}, nil
	}
	if err != nil {
		return OwnerStatus{}, err
	}
	return OwnerStatus{State: "claimed", ClaimedAt: claimedAt}, nil
}

// ClaimOwner makes subject, a signed-in account, the app's owner (its
// first account: FirstSubject, an admin) when token is the claim token.
// Compared in constant time; claimed in one conditional UPDATE, so of
// two claims at once one wins and the other gets ErrOwnerClaimed. A
// wrong token is ErrOwnerToken and changes nothing. On success the
// token file is removed and status.json says claimed.
func (a *Auth) ClaimOwner(ctx context.Context, subject, token string) error {
	if !a.cfg.OwnerClaim {
		return ErrOwnerClaimOff
	}
	if subject == "" {
		return errors.New("auth: ClaimOwner needs the signed-in subject")
	}
	var stored string
	var owner *string
	err := a.pool.QueryRow(ctx, `SELECT token_hash, subject FROM auth_owner_claim WHERE id = 'owner'`).Scan(&stored, &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrOwnerToken
	}
	if err != nil {
		return err
	}
	if owner != nil {
		return ErrOwnerClaimed
	}
	given := hashOwnerToken(strings.TrimSpace(token))
	if stored == "" || subtle.ConstantTimeCompare([]byte(given), []byte(stored)) != 1 {
		return ErrOwnerToken
	}
	tag, err := a.pool.Exec(ctx, `UPDATE auth_owner_claim SET subject = $1, claimed_at = now(), token_hash = '', updated_at = now()
		WHERE id = 'owner' AND subject IS NULL AND token_hash = $2`, subject, stored)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrOwnerClaimed
	}
	a.firstMu.Lock()
	a.first = subject
	a.firstMu.Unlock()
	a.removeClaimToken()
	now := time.Now().UTC()
	if err := a.writeOwnerStatus(OwnerStatus{State: "claimed", ClaimedAt: &now}); err != nil {
		slog.Error("auth: writing the owner claim status", "error", err)
	}
	return nil
}

// RotateOwnerToken replaces the claim token while the app has no owner:
// a new token file and stored hash; the old token stops working at once,
// on every node. With AUTH_OWNER_CLAIM_TOKEN the platform rotates it.
func (a *Auth) RotateOwnerToken(ctx context.Context) error {
	if !a.cfg.OwnerClaim {
		return ErrOwnerClaimOff
	}
	if a.cfg.OwnerClaimToken != "" {
		return errors.New("auth: the owner claim token comes from AUTH_OWNER_CLAIM_TOKEN: change it there and restart")
	}
	st, err := a.OwnerClaimStatus(ctx)
	if err != nil {
		return err
	}
	if st.State == "claimed" {
		return ErrOwnerClaimed
	}
	token, err := a.writeClaimToken(true)
	if err != nil {
		return err
	}
	tag, err := a.pool.Exec(ctx, `INSERT INTO auth_owner_claim (id, token_hash) VALUES ('owner', $1)
		ON CONFLICT (id) DO UPDATE SET token_hash = EXCLUDED.token_hash, updated_at = now() WHERE auth_owner_claim.subject IS NULL`, hashOwnerToken(token))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		a.removeClaimToken()
		return ErrOwnerClaimed
	}
	return a.writeOwnerStatus(OwnerStatus{State: "unclaimed"})
}

// ownerFirst is FirstSubject under the claim: the owner, "" until claimed.
func (a *Auth) ownerFirst(ctx context.Context) (string, error) {
	var subject *string
	err := a.pool.QueryRow(ctx, `SELECT subject FROM auth_owner_claim WHERE id = 'owner'`).Scan(&subject)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && subject == nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return *subject, nil
}
