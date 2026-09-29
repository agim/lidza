// Package credentials keeps an app's secrets encrypted at rest:
// config/credentials.yml.enc holds KEY: value pairs sealed with AES-256-GCM
// under the master key in config/master.key (never committed) or
// LIDZA_MASTER_KEY (production). Every pack reads them the way it reads
// .env: pkg/env merges the decrypted values between the .env files and
// the process environment, so MAIL_API_KEY in the credentials is
// MAIL_API_KEY to the mail pack. Values saved at runtime (the admin
// pages) are held in the database by the db pack, encrypted with the
// same key, and override the file.
//
// A value can be for one mode only: under a section named after the
// mode (dev:, production:) in the file, "dev.STRIPE_SECRET_KEY" to Set
// and Read. A mode reads the plain values and its own section, which
// wins; one sealed file holds a sandbox key for dev and the live key for
// production, and production needs only the master key.
package credentials

import (
	"bufio"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Files and the environment.
const (
	// MasterKeyFile holds the key, 32 bytes as 64 hex characters.
	MasterKeyFile = "config/master.key"
	// File is the sealed credentials.
	File = "config/credentials.yml.enc"
	// EnvMasterKey carries the key where no file should exist: production.
	EnvMasterKey = "LIDZA_MASTER_KEY"
)

// ErrKeyNotHere is Generate's refusal to make a key for a checkout whose
// credentials file was sealed with one it lacks: a clone, CI, another
// machine. A new key could not open the file.
var ErrKeyNotHere = errors.New("credentials: " + File + " is sealed with a master key this checkout lacks: put that key in " + MasterKeyFile + " or " + EnvMasterKey + " (from whoever set up the app); a new key could not open the file")

// ErrNoKey is returned when neither the file nor the variable has a key.
var ErrNoKey = errors.New("credentials: no master key: " + MasterKeyFile + " is missing and " + EnvMasterKey + " is not set (lidza credentials init creates one)")

// Key returns the master key: LIDZA_MASTER_KEY, else config/master.key.
func Key(dir string) ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(EnvMasterKey))
	if raw == "" {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(MasterKeyFile)))
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoKey
		}
		if err != nil {
			return nil, err
		}
		raw = strings.TrimSpace(string(data))
	}
	key, err := hex.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return nil, errors.New("credentials: the master key must be 32 bytes as 64 hex characters")
	}
	return key, nil
}

// HasKey reports whether a master key is available.
func HasKey(dir string) bool {
	_, err := Key(dir)
	return err == nil
}

// Generate writes a new master key to config/master.key (unless there is
// a key, in the file or LIDZA_MASTER_KEY), adds it to .gitignore, and
// seals an empty credentials file when there is none. It returns whether
// a key was created, and ErrKeyNotHere when the credentials file exists
// without its key: a new one would not open it.
func Generate(dir string) (bool, error) {
	p := filepath.Join(dir, filepath.FromSlash(MasterKeyFile))
	created := false
	_, statErr := os.Stat(p)
	if errors.Is(statErr, os.ErrNotExist) && os.Getenv(EnvMasterKey) == "" {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(File))); err == nil {
			return false, ErrKeyNotHere
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return false, err
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return false, err
		}
		if err := os.WriteFile(p, []byte(hex.EncodeToString(key)+"\n"), 0o600); err != nil {
			return false, err
		}
		created = true
	}
	if err := ignore(dir, MasterKeyFile); err != nil {
		return created, err
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(File))); errors.Is(err, os.ErrNotExist) {
		if err := Write(dir, map[string]string{}); err != nil {
			return created, err
		}
	}
	return created, nil
}

// ignore appends the path to .gitignore unless it is listed.
func ignore(dir, path string) error {
	p := filepath.Join(dir, ".gitignore")
	data, _ := os.ReadFile(p)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == path {
			return nil
		}
	}
	text := string(data)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return os.WriteFile(p, []byte(text+path+"\n"), 0o644)
}

// Read decrypts the credentials file, names as written: NAME, or
// mode.NAME for a value in a mode's section (Resolve picks a mode's).
// No file is no credentials.
func Read(dir string) (map[string]string, error) {
	key, err := Key(dir)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(File)))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	plain, err := Decrypt(key, strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("credentials: %s: %w (wrong master key?)", File, err)
	}
	return Parse(string(plain))
}

// Write seals values into the credentials file.
func Write(dir string, values map[string]string) error {
	key, err := Key(dir)
	if err != nil {
		return err
	}
	sealed, err := Encrypt(key, []byte(Format(values)))
	if err != nil {
		return err
	}
	p := filepath.Join(dir, filepath.FromSlash(File))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(sealed+"\n"), 0o644)
}

// Mode is the run mode the values resolve for: LIDZA_MODE, else
// "production" (pkg/env.Mode, which imports this package).
func Mode() string {
	if m := os.Getenv("LIDZA_MODE"); m != "" {
		return m
	}
	return "production"
}

// Resolve returns what mode reads from raw (Read's names): the plain
// names, with mode's "mode.NAME" entries over them; other modes'
// entries are left out.
func Resolve(raw map[string]string, mode string) map[string]string {
	out := map[string]string{}
	for k, v := range raw {
		if !strings.Contains(k, ".") {
			out[k] = v
		}
	}
	for k, v := range raw {
		if m, name, ok := strings.Cut(k, "."); ok && m == mode {
			out[name] = v
		}
	}
	return out
}

// Set stores one or more values in the file.
func Set(dir string, values map[string]string) error {
	cur, err := Read(dir)
	if err != nil {
		return err
	}
	for k, v := range values {
		if err := checkName(k); err != nil {
			return err
		}
		cur[k] = v
	}
	return Write(dir, cur)
}

// Unset removes names from the file.
func Unset(dir string, names ...string) error {
	cur, err := Read(dir)
	if err != nil {
		return err
	}
	for _, n := range names {
		delete(cur, n)
	}
	return Write(dir, cur)
}

// checkName accepts NAME, or mode.NAME for one mode only.
func checkName(k string) error {
	if mode, name, ok := strings.Cut(k, "."); ok {
		if err := checkMode(mode); err != nil {
			return err
		}
		k = name
	}
	if k == "" {
		return errors.New("credentials: empty name")
	}
	for _, r := range k {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return fmt.Errorf("credentials: %q: names are environment variable names (MAIL_API_KEY)", k)
		}
	}
	return nil
}

func checkMode(mode string) error {
	if mode == "development" {
		return errors.New(`credentials: the development mode is "dev" (dev.NAME, a dev: section)`)
	}
	if mode == "" {
		return errors.New("credentials: empty mode before the dot")
	}
	for i, r := range mode {
		if !(r >= 'a' && r <= 'z' || i > 0 && (r >= '0' && r <= '9' || r == '-' || r == '_')) {
			return fmt.Errorf("credentials: mode %q: lowercase, like dev or production", mode)
		}
	}
	return nil
}

// Encrypt seals plain with AES-256-GCM under key: base64 of nonce and
// ciphertext.
func Encrypt(key, plain []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(append(nonce, gcm.Seal(nil, nonce, plain, nil)...)), nil
}

// Decrypt opens what Encrypt sealed.
func Decrypt(key []byte, sealed string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize() {
		return nil, errors.New("sealed data too short")
	}
	return gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
}

// Parse reads the flat YAML the file holds: one `KEY: value` per line,
// values optionally double-quoted (Go syntax), # comments.
func Parse(text string) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(text))
	n := 0
	section := ""
	for sc.Scan() {
		n++
		raw := sc.Text()
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("credentials: line %d: expected KEY: value", n)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		indented := raw[0] == ' ' || raw[0] == '\t'
		switch {
		case !indented && v == "" && k != "" && k == strings.ToLower(k):
			// A mode's section: "dev:", its values indented below.
			if err := checkMode(k); err != nil {
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
			section = k
			continue
		case indented && section != "":
			if strings.Contains(k, ".") {
				return nil, fmt.Errorf("credentials: line %d: %s inside the %s: section", n, k, section)
			}
			k = section + "." + k
		case indented:
			return nil, fmt.Errorf("credentials: line %d: indented outside a mode's section", n)
		default:
			section = ""
		}
		if err := checkName(k); err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		if strings.HasPrefix(v, `"`) {
			u, err := strconv.Unquote(v)
			if err != nil {
				return nil, fmt.Errorf("credentials: line %d: bad quoted value", n)
			}
			v = u
		}
		out[k] = v
	}
	return out, sc.Err()
}

// Format writes values as the flat YAML Parse reads, names sorted.
func Format(values map[string]string) string {
	names := make([]string, 0, len(values))
	for k := range values {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# Secrets of this app, sealed with config/master.key. Edit with\n# lidza credentials set NAME=value; every pack reads them like .env.\n# A mode's section (dev:, production:) holds values for that mode\n# only, over the plain ones: lidza credentials set dev.NAME=value.\n")
	quote := func(v string) string {
		if v == "" || strings.ContainsAny(v, "#:\"\n\\") || v != strings.TrimSpace(v) {
			return strconv.Quote(v)
		}
		return v
	}
	sections := map[string][]string{}
	var modes []string
	for _, k := range names {
		if mode, name, ok := strings.Cut(k, "."); ok {
			if sections[mode] == nil {
				modes = append(modes, mode)
			}
			sections[mode] = append(sections[mode], name)
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", k, quote(values[k]))
	}
	sort.Strings(modes)
	for _, mode := range modes {
		fmt.Fprintf(&b, "\n%s:\n", mode)
		for _, name := range sections[mode] {
			fmt.Fprintf(&b, "  %s: %s\n", name, quote(values[mode+"."+name]))
		}
	}
	return b.String()
}

// Store keeps values saved while the app runs, encrypted with the master
// key; the db pack provides one in Postgres. Save and Delete also update
// the runtime overrides.
type Store interface {
	Save(ctx context.Context, name, value string) error
	Delete(ctx context.Context, name string) error
	// Names lists what the store holds, sorted.
	Names(ctx context.Context) ([]string, error)
}

// Runtime overrides: values saved while the app runs (the admin pages),
// kept by the db pack and merged over the file.
var (
	mu        sync.RWMutex
	overrides = map[string]string{}
)

// SetOverrides replaces the runtime values.
func SetOverrides(values map[string]string) {
	mu.Lock()
	defer mu.Unlock()
	overrides = map[string]string{}
	for k, v := range values {
		overrides[k] = v
	}
}

// SetOverride sets one runtime value ("" removes it).
func SetOverride(name, value string) {
	mu.Lock()
	defer mu.Unlock()
	if value == "" {
		delete(overrides, name)
		return
	}
	overrides[name] = value
}

// Overrides returns a copy of the runtime values.
func Overrides() map[string]string {
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]string, len(overrides))
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

// Values returns the file's values for the run mode (Resolve) with the
// runtime overrides applied; without a master key or a file, the
// overrides alone. This is what pkg/env merges.
func Values(dir string) map[string]string {
	out := map[string]string{}
	if file, err := Read(dir); err == nil {
		out = Resolve(file, Mode())
	}
	for k, v := range Overrides() {
		out[k] = v
	}
	return out
}

// Names lists the names in the file and the overrides, sorted, values
// withheld.
func Names(dir string) []string {
	vals := Values(dir)
	names := make([]string, 0, len(vals))
	for k := range vals {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
