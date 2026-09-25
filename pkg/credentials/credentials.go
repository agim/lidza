// Package credentials keeps an app's secrets encrypted at rest:
// config/credentials.yml.enc holds KEY: value pairs sealed with AES-256-GCM
// under the master key in config/master.key (never committed) or
// LIDZA_MASTER_KEY (production). Every pack reads them the way it reads
// .env: pkg/env merges the decrypted values between the .env files and
// the process environment, so MAIL_API_KEY in the credentials is
// MAIL_API_KEY to the mail pack. Values saved at runtime (the admin
// pages) are held in the database by the db pack, encrypted with the
// same key, and override the file.
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

// Generate writes a new master key to config/master.key (unless one
// exists), adds it to .gitignore, and seals an empty credentials file
// when there is none. It returns whether a key was created.
func Generate(dir string) (bool, error) {
	p := filepath.Join(dir, filepath.FromSlash(MasterKeyFile))
	created := false
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
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

// Read decrypts the credentials file; no file is no credentials.
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

func checkName(k string) error {
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
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("credentials: line %d: expected KEY: value", n)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
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
	b.WriteString("# Secrets of this app, sealed with config/master.key. Edit with\n# lidza credentials set NAME=value; every pack reads them like .env.\n")
	for _, k := range names {
		v := values[k]
		if v == "" || strings.ContainsAny(v, "#:\"\n\\") || v != strings.TrimSpace(v) {
			v = strconv.Quote(v)
		}
		fmt.Fprintf(&b, "%s: %s\n", k, v)
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

// Values returns the file's values with the runtime overrides applied;
// without a master key or a file, the overrides alone. This is what
// pkg/env merges.
func Values(dir string) map[string]string {
	out := map[string]string{}
	if file, err := Read(dir); err == nil {
		for k, v := range file {
			out[k] = v
		}
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
