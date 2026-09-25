// Package validate holds the rules generated Validate methods call and the
// error type they return. It has no state.
package validate

import (
	"encoding/json"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// FieldError is one failed rule on one field.
type FieldError struct {
	// Field is the JSON name of the field.
	Field string `json:"field"`
	// Rule is the rule that failed: required, min, max, email, url,
	// pattern, enum.
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// Errors is the error a Validate method returns when any rule fails. Its
// JSON form is what handlers send with status 422.
type Errors struct {
	Fields []FieldError `json:"fields"`
}

func (e *Errors) Error() string {
	parts := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		parts[i] = f.Field + ": " + f.Message
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

// MarshalJSON renders {"error": "validation", "fields": [...]}.
func (e *Errors) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Error  string       `json:"error"`
		Fields []FieldError `json:"fields"`
	}{"validation", e.Fields})
}

// Add records a failure. It is safe on a nil *Errors.
func (e *Errors) Add(field, rule, message string) {
	e.Fields = append(e.Fields, FieldError{field, rule, message})
}

// Result returns e as an error when it has failures, else nil.
func (e *Errors) Result() error {
	if e == nil || len(e.Fields) == 0 {
		return nil
	}
	return e
}

// Validator is implemented by generated types; the typed router calls it
// on request bodies.
type Validator interface {
	Validate() error
}

// Email reports whether s is a single address without a display name.
func Email(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s
}

// URL reports whether s is an absolute http(s) URL.
func URL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

var (
	patternsMu sync.Mutex
	patterns   = map[string]*regexp.Regexp{}
)

// Pattern reports whether s matches the anchored regular expression re.
// Compiled expressions are cached; the set is bounded by the patterns in
// the schema.
func Pattern(re, s string) bool {
	patternsMu.Lock()
	rx, ok := patterns[re]
	if !ok {
		var err error
		rx, err = regexp.Compile("^(?:" + re + ")$")
		if err != nil {
			patternsMu.Unlock()
			return false
		}
		patterns[re] = rx
	}
	patternsMu.Unlock()
	return rx.MatchString(s)
}
