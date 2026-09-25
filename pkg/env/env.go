// Package env fills a configuration struct from the environment, so
// secrets and per-deployment settings never live in lidza.json or code.
//
//	type Config struct {
//		DatabaseURL string        `env:"DATABASE_URL" required:"true"`
//		Workers     int           `env:"WORKERS" default:"4"`
//		Timeout     time.Duration `env:"TIMEOUT" default:"30s"`
//		Debug       bool          `env:"DEBUG"`
//		Origins     []string      `env:"CORS_ORIGINS"`
//	}
//
// Load reads, in order of increasing precedence: .env, .env.<mode>, the
// process environment. Mode is LIDZA_MODE ("dev" under lidza dev, "test"
// under lidza test), else "production".
package env

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Mode returns the run mode: the value of LIDZA_MODE, or "production".
func Mode() string {
	if m := os.Getenv("LIDZA_MODE"); m != "" {
		return m
	}
	return "production"
}

// Load fills dst (a pointer to a struct) from .env files in dir and the
// process environment. Missing required variables and unparsable values
// are reported together in one error.
func Load(dir string, dst any) error {
	values := map[string]string{}
	for _, name := range []string{".env", ".env." + Mode()} {
		if err := readFile(filepath.Join(dir, name), values); err != nil {
			return err
		}
	}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			values[k] = v
		}
	}
	return Fill(dst, values)
}

// Fill sets dst's fields from values, using the `env`, `default` and
// `required` tags. Exported.
func Fill(dst any, values map[string]string) error {
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.Elem().Kind() != reflect.Struct {
		return errors.New("env: Load needs a pointer to a struct")
	}
	var errs []string
	rv = rv.Elem()
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		key := f.Tag.Get("env")
		if key == "" || !f.IsExported() {
			continue
		}
		raw, ok := values[key]
		if !ok || raw == "" {
			raw, ok = f.Tag.Lookup("default")
			if !ok {
				if f.Tag.Get("required") == "true" {
					errs = append(errs, key+" is required")
				}
				continue
			}
		}
		if err := set(rv.Field(i), raw); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", key, err))
		}
	}
	if len(errs) > 0 {
		return errors.New("env: " + strings.Join(errs, "; "))
	}
	return nil
}

func set(v reflect.Value, raw string) error {
	switch v.Kind() {
	case reflect.String:
		v.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("expected true or false, got %q", raw)
		}
		v.SetBool(b)
	case reflect.Int, reflect.Int64, reflect.Int32:
		if v.Type() == reflect.TypeOf(time.Duration(0)) {
			d, err := time.ParseDuration(raw)
			if err != nil {
				return fmt.Errorf("expected a duration such as 30s, got %q", raw)
			}
			v.SetInt(int64(d))
			return nil
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("expected an integer, got %q", raw)
		}
		v.SetInt(n)
	case reflect.Float64:
		x, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("expected a number, got %q", raw)
		}
		v.SetFloat(x)
	case reflect.Slice:
		if v.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("unsupported slice type %s", v.Type())
		}
		var parts []string
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
		v.Set(reflect.ValueOf(parts))
	default:
		return fmt.Errorf("unsupported type %s", v.Type())
	}
	return nil
}

// readFile parses KEY=value lines (with # comments, optional quotes and
// "export " prefixes) into values; a missing file is not an error.
func readFile(path string, values map[string]string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected KEY=value", path, n)
		}
		values[strings.TrimSpace(k)] = unquote(strings.TrimSpace(v))
	}
	return sc.Err()
}

// unquote strips matching quotes from a value, or an inline " # comment"
// from an unquoted one.
func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
		if end := strings.IndexByte(v[1:], v[0]); end >= 0 {
			return v[1 : end+1]
		}
	}
	if i := strings.Index(v, " #"); i >= 0 {
		return strings.TrimSpace(v[:i])
	}
	return v
}
