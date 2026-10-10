package auth

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Tokens, sealed cookies, stored hashes and provider keys are read from
// outside the process: whatever they hold, reading them never panics and
// nothing forged is accepted.

func fuzzAuth(t testing.TB, secret string) *Auth {
	a, err := New(Config{Secret: secret}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func FuzzVerify(f *testing.F) {
	a := fuzzAuth(f, testSecret)
	tokens, err := a.tokens("u1", map[string]any{"role": "admin"}, "s1", "")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(tokens.Access)
	f.Add("eyJhbGciOiJub25lIn0.eyJzdWIiOiJ1MSJ9.")
	f.Add("a.b.c")
	other := fuzzAuth(f, strings.Repeat("z", 32))
	f.Fuzz(func(t *testing.T, token string) {
		_, errA := a.Verify(token)
		_, errB := other.Verify(token)
		if errA == nil && errB == nil {
			t.Fatalf("a token verified under two secrets: %q", token)
		}
	})
}

func FuzzOpen(f *testing.F) {
	a := fuzzAuth(f, testSecret)
	sealed, _ := a.seal(roundTrip{Provider: "google", Expires: time.Now().Add(time.Hour).Unix()})
	f.Add(sealed)
	f.Add("e30.AAAA")
	f.Add(".")
	other := fuzzAuth(f, strings.Repeat("z", 32))
	f.Fuzz(func(t *testing.T, value string) {
		_, errA := a.openTrip(value)
		_, errB := other.openTrip(value)
		if errA == nil && errB == nil {
			t.Fatalf("a cookie opened under two secrets: %q", value)
		}
	})
}

// FuzzCheckPassword reads stored hashes: a damaged or hostile row is a
// mismatch, never a panic or an unbounded hash.
func FuzzCheckPassword(f *testing.F) {
	good, err := HashPassword("correct horse")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(good, "correct horse")
	f.Add("$argon2id$v=19$m=65536,t=3,p=0$AAAA$AAAA", "x")
	f.Add("$argon2id$v=19$m=8,t=1,p=1$AAAA$", "x")
	f.Add("$argon2id$v=19$m=4294967295,t=4294967295,p=255$AAAA$AAAA", "x")
	f.Fuzz(func(t *testing.T, encoded, password string) {
		if CheckPassword(encoded, password) && encoded != good {
			// Only a well-formed hash of this password may match.
			if !strings.HasPrefix(encoded, "$argon2id$") {
				t.Fatalf("matched a malformed hash %q", encoded)
			}
		}
	})
}

func FuzzParseJWK(f *testing.F) {
	f.Add(`{"kty":"RSA","n":"AQAB","e":"AQAB"}`)
	f.Add(`{"kty":"RSA","n":"","e":""}`)
	f.Add(`{"kty":"EC","crv":"P-256","x":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","y":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`)
	f.Fuzz(func(t *testing.T, raw string) {
		var jwk map[string]any
		if json.Unmarshal([]byte(raw), &jwk) != nil {
			return
		}
		key, err := parseJWK(jwk)
		if err != nil {
			return
		}
		// A key that parses must survive verifying a signature.
		digest := sha256.Sum256([]byte("payload"))
		switch k := key.(type) {
		case *rsa.PublicKey:
			rsa.VerifyPKCS1v15(k, crypto.SHA256, digest[:], make([]byte, 256))
		case *ecdsa.PublicKey:
			ecdsa.VerifyASN1(k, digest[:], []byte{0x30, 0})
		}
	})
}
