package auth

import (
	"strings"
	"testing"
	"time"
)

func TestTOTPAndBackupCodes(t *testing.T) {
	secret, err := newTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	code, err := totpCode(secret, totpStep(now))
	if err != nil {
		t.Fatal(err)
	}
	step, ok := verifyTOTP(secret, code, now, 0)
	if !ok || step != totpStep(now) {
		t.Fatalf("valid code refused: %d %v", step, ok)
	}
	if _, ok := verifyTOTP(secret, code, now, step); ok {
		t.Fatal("one time step was accepted twice")
	}
	if _, ok := verifyTOTP(secret, "000000", now, 0); ok {
		t.Fatal("wrong code accepted")
	}
	if got := totpURL(secret, "person@example.com", "Example"); !strings.HasPrefix(got, "otpauth://totp/Example:person@example.com?") || !strings.Contains(got, "secret="+secret) {
		t.Fatalf("otpauth URL: %s", got)
	}

	codes, hashes, err := newBackupCodes()
	if err != nil || len(codes) != backupCodeCount || len(hashes) != backupCodeCount {
		t.Fatalf("backup codes: %d %d %v", len(codes), len(hashes), err)
	}
	left, ok := useBackupCode(hashes, strings.ToUpper(codes[0]))
	if !ok || len(left) != len(hashes)-1 {
		t.Fatal("backup code refused")
	}
	if _, ok := useBackupCode(left, codes[0]); ok {
		t.Fatal("used backup code accepted again")
	}
}

func FuzzVerifyTOTP(f *testing.F) {
	secret, _ := newTOTPSecret()
	f.Add(secret, "123456", int64(1_700_000_000), int64(0))
	f.Add("bad", "", int64(0), int64(0))
	f.Fuzz(func(t *testing.T, secret, code string, unix, after int64) {
		verifyTOTP(secret, code, time.Unix(unix, 0), after)
	})
}
