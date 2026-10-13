package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

const (
	totpPeriod      = 30 * time.Second
	totpDigits      = 6
	totpSkew        = 1
	backupCodeCount = 10
)

var totpBase32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func newTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return totpBase32.EncodeToString(b), nil
}

func totpCode(secret string, step int64) (string, error) {
	key, err := totpBase32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, v%1_000_000), nil
}

func totpStep(t time.Time) int64 { return t.Unix() / int64(totpPeriod/time.Second) }

func verifyTOTP(secret, code string, now time.Time, after int64) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return 0, false
	}
	cur := totpStep(now)
	for step := cur - totpSkew; step <= cur+totpSkew; step++ {
		if step <= after {
			continue
		}
		want, err := totpCode(secret, step)
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

func totpURL(secret, account, issuer string) string {
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", fmt.Sprint(totpDigits))
	v.Set("period", fmt.Sprint(int(totpPeriod/time.Second)))
	return "otpauth://totp/" + url.PathEscape(issuer) + ":" + url.PathEscape(account) + "?" + v.Encode()
}

func totpQRCodeDataURL(otpAuthURL string) (string, error) {
	png, err := qrcode.Encode(otpAuthURL, qrcode.Medium, 256)
	if err != nil {
		return "", fmt.Errorf("auth: render authenticator QR code: %w", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

func newBackupCodes() (codes, hashes []string, err error) {
	for range backupCodeCount {
		b := make([]byte, 5)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, err
		}
		s := strings.ToLower(totpBase32.EncodeToString(b))
		code := s[:4] + "-" + s[4:]
		codes = append(codes, code)
		hashes = append(hashes, hashBackupCode(code))
	}
	return codes, hashes, nil
}

func hashBackupCode(code string) string {
	normalized := strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
	sum := sha256.Sum256([]byte("lidza backup code " + normalized))
	return hex.EncodeToString(sum[:])
}

func useBackupCode(hashes []string, code string) ([]string, bool) {
	want := hashBackupCode(code)
	for i, hash := range hashes {
		if subtle.ConstantTimeCompare([]byte(hash), []byte(want)) == 1 {
			return append(append([]string{}, hashes[:i]...), hashes[i+1:]...), true
		}
	}
	return hashes, false
}
