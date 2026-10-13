package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/agim/lidza"
	"github.com/agim/lidza/pkg/credentials"
	"github.com/agim/lidza/pkg/router"
	"github.com/agim/lidza/pkg/validate"
)

// OTPHeader carries an authenticator or recovery code on password sign-in.
// Provider sign-in relies on the provider's own second factor.
const OTPHeader = "X-Lidza-OTP"

// StepUpWindow is the maximum age of a purpose- and target-bound second
// factor grant.
const StepUpWindow = 15 * time.Minute

const (
	factorMaxFailures = 10
	factorFailWindow  = 15 * time.Minute
)

var (
	ErrFactorSetupRequired = router.ErrorCode(http.StatusForbidden, "factor_setup_required", "turn on two-factor authentication before using this action")
	ErrFactorCodeRequired  = router.ErrorCode(http.StatusForbidden, "factor_code_required", "enter a current authenticator or recovery code")
	ErrFactorCodeInvalid   = router.ErrorCode(http.StatusUnauthorized, "factor_code_invalid", "that authenticator or recovery code is not valid")
	ErrFactorLocked        = router.ErrorCode(http.StatusTooManyRequests, "factor_locked", "too many wrong codes; try again in 15 minutes")
	ErrStepUpRequired      = router.ErrorCode(http.StatusForbidden, "step_up_required", "a fresh second-factor check is required for this action")
)

// TwoFactorStatus says whether the current account has an authenticator
// factor and how many single-use recovery codes remain.
type TwoFactorStatus struct {
	Enabled         bool `json:"enabled"`
	BackupCodesLeft int  `json:"backupCodesLeft"`
}

// TwoFactorSetup is shown once while an authenticator factor is enrolled.
type TwoFactorSetup struct {
	Secret     string `json:"secret"`
	OTPAuthURL string `json:"otpauthUrl"`
}

// TwoFactorCode carries an authenticator or recovery code.
type TwoFactorCode struct {
	Code string `json:"code"`
}

func (c TwoFactorCode) Validate() error {
	var errs validate.Errors
	if strings.TrimSpace(c.Code) == "" {
		errs.Add("code", "required", "is required")
	} else if len(c.Code) > 64 {
		errs.Add("code", "max", "must be at most 64 characters")
	}
	return errs.Result()
}

// BackupCodes are recovery codes shown only when they are generated.
type BackupCodes struct {
	Codes []string `json:"codes"`
}

// StepUpRequest checks a fresh factor for exactly one operation. Purpose is
// a stable operation name; Target is the one resource it will access.
type StepUpRequest struct {
	Code    string `json:"code"`
	Purpose string `json:"purpose"`
	Target  string `json:"target,omitempty"`
}

func (s StepUpRequest) Validate() error {
	var errs validate.Errors
	if strings.TrimSpace(s.Code) == "" {
		errs.Add("code", "required", "is required")
	}
	if strings.TrimSpace(s.Purpose) == "" {
		errs.Add("purpose", "required", "is required")
	} else if len(s.Purpose) > 100 {
		errs.Add("purpose", "max", "must be at most 100 characters")
	}
	if len(s.Target) > 300 {
		errs.Add("target", "max", "must be at most 300 characters")
	}
	return errs.Result()
}

// StepUpGrant is the expiry of the session-bound grant.
type StepUpGrant struct {
	Purpose   string    `json:"purpose"`
	Target    string    `json:"target,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type factorRow struct {
	sealed      string
	lastStep    int64
	backupCodes []string
	enabledAt   *time.Time
	failedCount int
	failedAt    *time.Time
}

func (a *Auth) factor(ctx context.Context, subject string) (factorRow, error) {
	var row factorRow
	err := a.pool.QueryRow(ctx, `SELECT secret_sealed, last_step, backup_codes, enabled_at, failed_count, failed_at FROM auth_factor WHERE subject = $1`, subject).
		Scan(&row.sealed, &row.lastStep, &row.backupCodes, &row.enabledAt, &row.failedCount, &row.failedAt)
	return row, err
}

func factorEnvelope(subject, secret string) []byte {
	return []byte("lidza:totp:" + subject + "\n" + secret)
}

func sealFactor(subject, secret string) (string, error) {
	key, err := credentials.Key(".")
	if err != nil {
		return "", fmt.Errorf("two-factor authentication needs LIDZA_MASTER_KEY: %w", err)
	}
	return credentials.Encrypt(key, factorEnvelope(subject, secret))
}

func openFactor(subject, sealed string) (string, error) {
	key, err := credentials.Key(".")
	if err != nil {
		return "", err
	}
	plain, err := credentials.Decrypt(key, sealed)
	if err != nil {
		return "", err
	}
	prefix := "lidza:totp:" + subject + "\n"
	if !strings.HasPrefix(string(plain), prefix) {
		return "", errors.New("auth: two-factor secret belongs to another account")
	}
	return strings.TrimPrefix(string(plain), prefix), nil
}

// TwoFactorEnabled reports whether subject has completed enrollment.
func (a *Auth) TwoFactorEnabled(ctx context.Context, subject string) (bool, error) {
	row, err := a.factor(ctx, subject)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return row.enabledAt != nil, err
}

// CheckSecondFactor consumes a current authenticator time step or one
// recovery code. A code cannot win two concurrent checks.
func (a *Auth) CheckSecondFactor(ctx context.Context, subject, code string) error {
	row, err := a.factor(ctx, subject)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && row.enabledAt == nil {
		return ErrFactorSetupRequired
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(code) == "" {
		return ErrFactorCodeRequired
	}
	now := lidza.Now(ctx)
	if row.failedAt != nil && now.Sub(*row.failedAt) < factorFailWindow && row.failedCount >= factorMaxFailures {
		return ErrFactorLocked
	}
	secret, err := openFactor(subject, row.sealed)
	if err != nil {
		return err
	}
	if step, ok := verifyTOTP(secret, code, now, row.lastStep); ok {
		tag, err := a.pool.Exec(ctx, `UPDATE auth_factor SET last_step = $2, failed_count = 0, failed_at = NULL, updated_at = now() WHERE subject = $1 AND last_step < $2`, subject, step)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			return nil
		}
		return a.factorFailed(ctx, subject, now)
	}
	if left, ok := useBackupCode(row.backupCodes, code); ok {
		tag, err := a.pool.Exec(ctx, `UPDATE auth_factor SET backup_codes = $2, failed_count = 0, failed_at = NULL, updated_at = now() WHERE subject = $1 AND backup_codes = $3`, subject, left, row.backupCodes)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			return nil
		}
	}
	return a.factorFailed(ctx, subject, now)
}

func (a *Auth) factorFailed(ctx context.Context, subject string, now time.Time) error {
	_, err := a.pool.Exec(ctx, `UPDATE auth_factor SET
failed_count = CASE WHEN failed_at IS NULL OR failed_at < $2 THEN 1 ELSE failed_count + 1 END,
failed_at = CASE WHEN failed_at IS NULL OR failed_at < $2 THEN $3 ELSE failed_at END,
updated_at = now() WHERE subject = $1`, subject, now.Add(-factorFailWindow), now)
	if err != nil {
		return err
	}
	return ErrFactorCodeInvalid
}

func (s *signin) authTwoFactorStatus(ctx context.Context, _ *router.Request[router.None]) (TwoFactorStatus, error) {
	row, err := From(ctx).factor(ctx, CurrentUser(ctx).ID)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && row.enabledAt == nil {
		return TwoFactorStatus{}, nil
	}
	if err != nil {
		return TwoFactorStatus{}, err
	}
	return TwoFactorStatus{Enabled: true, BackupCodesLeft: len(row.backupCodes)}, nil
}

func (s *signin) authTwoFactorSetup(ctx context.Context, _ *router.Request[router.None]) (TwoFactorSetup, error) {
	a := From(ctx)
	u := CurrentUser(ctx)
	if on, err := a.TwoFactorEnabled(ctx, u.ID); err != nil {
		return TwoFactorSetup{}, err
	} else if on {
		return TwoFactorSetup{}, router.ErrorCode(http.StatusConflict, "factor_enabled", "two-factor authentication is already on; disable it before enrolling another authenticator")
	}
	secret, err := newTOTPSecret()
	if err != nil {
		return TwoFactorSetup{}, err
	}
	sealed, err := sealFactor(u.ID, secret)
	if err != nil {
		return TwoFactorSetup{}, err
	}
	if _, err := a.pool.Exec(ctx, `INSERT INTO auth_factor (subject, secret_sealed) VALUES ($1, $2)
ON CONFLICT (subject) DO UPDATE SET secret_sealed = EXCLUDED.secret_sealed, last_step = 0, backup_codes = '{}', enabled_at = NULL, failed_count = 0, failed_at = NULL, updated_at = now()`, u.ID, sealed); err != nil {
		return TwoFactorSetup{}, err
	}
	p, err := a.Profile(ctx, u.ID)
	if err != nil {
		return TwoFactorSetup{}, err
	}
	issuer := strings.TrimSpace(s.opt.TwoFactorIssuer)
	if issuer == "" {
		issuer = strings.TrimSpace(s.opt.Title)
	}
	if issuer == "" {
		issuer = "Lidza"
	}
	account := p.Email
	if account == "" {
		account = p.Subject
	}
	return TwoFactorSetup{Secret: secret, OTPAuthURL: totpURL(secret, account, issuer)}, nil
}

func (s *signin) authTwoFactorEnable(ctx context.Context, req *router.Request[TwoFactorCode]) (BackupCodes, error) {
	a := From(ctx)
	subject := CurrentUser(ctx).ID
	row, err := a.factor(ctx, subject)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && row.enabledAt != nil {
		return BackupCodes{}, router.ErrorCode(http.StatusConflict, "factor_setup_not_started", "start two-factor setup first")
	}
	if err != nil {
		return BackupCodes{}, err
	}
	secret, err := openFactor(subject, row.sealed)
	if err != nil {
		return BackupCodes{}, err
	}
	step, ok := verifyTOTP(secret, req.Body.Code, lidza.Now(ctx), 0)
	if !ok {
		return BackupCodes{}, ErrFactorCodeInvalid
	}
	codes, hashes, err := newBackupCodes()
	if err != nil {
		return BackupCodes{}, err
	}
	tag, err := a.pool.Exec(ctx, `UPDATE auth_factor SET last_step = $2, backup_codes = $3, enabled_at = now(), failed_count = 0, failed_at = NULL, updated_at = now() WHERE subject = $1 AND enabled_at IS NULL`, subject, step, hashes)
	if err != nil {
		return BackupCodes{}, err
	}
	if tag.RowsAffected() != 1 {
		return BackupCodes{}, router.ErrorCode(http.StatusConflict, "factor_setup_not_started", "start two-factor setup first")
	}
	s.event(ctx, req.Raw, Event{Kind: EventTwoFactorEnabled, Subject: subject})
	return BackupCodes{Codes: codes}, nil
}

func (s *signin) authTwoFactorDisable(ctx context.Context, req *router.Request[TwoFactorCode]) (router.None, error) {
	a := From(ctx)
	subject := CurrentUser(ctx).ID
	if err := a.CheckSecondFactor(ctx, subject, req.Body.Code); err != nil {
		return router.None{}, err
	}
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return router.None{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM auth_step_up WHERE subject = $1`, subject); err != nil {
		return router.None{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM auth_factor WHERE subject = $1`, subject); err != nil {
		return router.None{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return router.None{}, err
	}
	s.event(ctx, req.Raw, Event{Kind: EventTwoFactorDisabled, Subject: subject})
	return router.None{}, nil
}

func (s *signin) authBackupCodes(ctx context.Context, req *router.Request[TwoFactorCode]) (BackupCodes, error) {
	a := From(ctx)
	subject := CurrentUser(ctx).ID
	if err := a.CheckSecondFactor(ctx, subject, req.Body.Code); err != nil {
		return BackupCodes{}, err
	}
	codes, hashes, err := newBackupCodes()
	if err != nil {
		return BackupCodes{}, err
	}
	if _, err := a.pool.Exec(ctx, `UPDATE auth_factor SET backup_codes = $2, updated_at = now() WHERE subject = $1`, subject, hashes); err != nil {
		return BackupCodes{}, err
	}
	s.event(ctx, req.Raw, Event{Kind: EventBackupCodesChanged, Subject: subject})
	return BackupCodes{Codes: codes}, nil
}

func (s *signin) authStepUp(ctx context.Context, req *router.Request[StepUpRequest]) (StepUpGrant, error) {
	a := From(ctx)
	u := CurrentUser(ctx)
	if u.SessionID == "" {
		s.event(ctx, req.Raw, Event{Kind: EventStepUpRejected, Subject: u.ID, Purpose: req.Body.Purpose, Target: req.Body.Target, Reason: "session_required"})
		return StepUpGrant{}, router.ErrorCode(http.StatusForbidden, "session_required", "step-up authentication requires a signed-in browser session")
	}
	if err := a.CheckSecondFactor(ctx, u.ID, req.Body.Code); err != nil {
		s.event(ctx, req.Raw, Event{Kind: EventStepUpRejected, Subject: u.ID, Purpose: req.Body.Purpose, Target: req.Body.Target, Reason: factorReason(err)})
		return StepUpGrant{}, err
	}
	now := lidza.Now(ctx)
	until := now.Add(StepUpWindow)
	if _, err := a.pool.Exec(ctx, `DELETE FROM auth_step_up WHERE expires_at <= $1`, now); err != nil {
		return StepUpGrant{}, err
	}
	_, err := a.pool.Exec(ctx, `INSERT INTO auth_step_up (subject, session_id, purpose, target, expires_at) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (session_id, purpose, target) DO UPDATE SET subject = EXCLUDED.subject, expires_at = EXCLUDED.expires_at, created_at = now()`, u.ID, u.SessionID, strings.TrimSpace(req.Body.Purpose), strings.TrimSpace(req.Body.Target), until)
	if err != nil {
		return StepUpGrant{}, err
	}
	s.event(ctx, req.Raw, Event{Kind: EventStepUpGranted, Subject: u.ID, Purpose: req.Body.Purpose, Target: req.Body.Target})
	return StepUpGrant{Purpose: strings.TrimSpace(req.Body.Purpose), Target: strings.TrimSpace(req.Body.Target), ExpiresAt: until}, nil
}

func factorReason(err error) string {
	switch {
	case errors.Is(err, ErrFactorSetupRequired):
		return "setup_required"
	case errors.Is(err, ErrFactorCodeRequired):
		return "code_required"
	case errors.Is(err, ErrFactorLocked):
		return "locked"
	default:
		return "invalid"
	}
}

// CheckStepUp reports whether the current authenticated session has a live
// grant for exactly purpose and target. It never accepts an account-wide or
// differently targeted grant.
func (a *Auth) CheckStepUp(ctx context.Context, purpose, target string) error {
	u := CurrentUser(ctx)
	if u == nil || u.SessionID == "" {
		return ErrStepUpRequired
	}
	var expires time.Time
	err := a.pool.QueryRow(ctx, `SELECT expires_at FROM auth_step_up WHERE subject = $1 AND session_id = $2 AND purpose = $3 AND target = $4`, u.ID, u.SessionID, purpose, target).Scan(&expires)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !lidza.Now(ctx).Before(expires) {
		return ErrStepUpRequired
	}
	return err
}

// AuthorizeStepUp is the server-side gate for a sensitive operation. It
// checks an exact purpose and target and emits an audit-ready auth event for
// both allowed and rejected attempts. Target must identify the resource and
// must never contain the protected value.
func AuthorizeStepUp(ctx context.Context, r *http.Request, purpose, target string) error {
	a := From(ctx)
	err := a.CheckStepUp(ctx, purpose, target)
	u := CurrentUser(ctx)
	e := Event{Kind: EventStepUpAuthorized, Purpose: purpose, Target: target, Request: r}
	if u != nil {
		e.Subject = u.ID
	}
	if err != nil {
		e.Kind = EventStepUpRejected
		e.Reason = "grant_required"
	}
	mountedMu.Lock()
	s := mounted
	mountedMu.Unlock()
	if s != nil {
		s.event(ctx, r, e)
	}
	return err
}
