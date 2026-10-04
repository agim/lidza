// Package secrets recognises the secrets providers issue (API keys,
// tokens, private keys, database URLs with a password) in a string: what
// lidza check refuses in code (L010) and what the audit pack redacts.
package secrets

import (
	"regexp"
	"strings"
)

// shapes are the token formats providers issue.
var shapes = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"an AWS access key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"an Anthropic API key", regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{20,}`)},
	{"an OpenAI API key", regexp.MustCompile(`sk-(proj-)?[A-Za-z0-9_-]{20,}`)},
	{"a Google API key", regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`)},
	{"a SendGrid API key", regexp.MustCompile(`SG\.[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,}`)},
	{"a Mailgun API key", regexp.MustCompile(`key-[0-9a-f]{32}`)},
	{"a Resend API key", regexp.MustCompile(`re_[A-Za-z0-9]{20,}`)},
	{"a Slack token", regexp.MustCompile(`xox[abpr]-[A-Za-z0-9-]{10,}`)},
	{"a GitHub token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{30,}`)},
	{"a private key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	// Test keys are secrets too; publishable keys (pk_) are public.
	{"a Stripe secret key", regexp.MustCompile(`\b[rs]k_(live|test)_[A-Za-z0-9]{20,}`)},
	{"a Stripe webhook secret", regexp.MustCompile(`\bwhsec_[A-Za-z0-9+/]{24,}`)},
	// Header and payload are JSON objects, so both start with eyJ.
	{"a JSON Web Token", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{20,}`)},
}

// databaseURL matches a Postgres, MySQL or Redis URL with a password in
// it; the password is the second group. Interpolations (%s, ${x}) are not
// passwords.
var databaseURL = regexp.MustCompile(`\b(postgres|postgresql|mysql|redis|rediss)://[^\s:/@'"` + "`" + `%$\{}]*:([^\s/@'"` + "`" + `%$\{}]+)@`)

// placeholderPasswords are the stand-ins examples use.
var placeholderPasswords = map[string]bool{"pass": true, "password": true, "pwd": true, "secret": true, "changeme": true, "passwd": true}

// At names the kind of secret found in s and its byte offset, or
// "" and -1.
func At(s string) (string, int) {
	for _, shape := range shapes {
		if loc := shape.re.FindStringIndex(s); loc != nil {
			return shape.kind, loc[0]
		}
	}
	for _, m := range databaseURL.FindAllStringSubmatchIndex(s, -1) {
		pw := strings.ToLower(s[m[4]:m[5]])
		if placeholderPasswords[pw] || strings.Trim(pw, "x*.") == "" || strings.HasPrefix(pw, "<") {
			continue
		}
		return "a database URL with its password", m[0]
	}
	return "", -1
}

// Kind names the secret s holds ("a GitHub token"), "" when none.
func Kind(s string) string {
	kind, _ := At(s)
	return kind
}
