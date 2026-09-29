// Package decimal is the Go type of a schema.lidza decimal(p, s) field,
// a Postgres numeric(p, s) column: an exact decimal number kept as its
// text, "12.50", never as a float. JSON carries it as a string, so no
// client parses it into a binary float on the way; pgx reads and writes
// it as numeric, and sqlc maps numeric columns to it.
//
// Arithmetic goes through math/big: Rat gives the exact value, FromRat
// rounds a result back to a scale.
//
//	total := new(big.Rat).Mul(price.Rat(), big.NewRat(int64(qty), 1))
//	order.Total = decimal.FromRat(total, 2)
package decimal

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// Decimal is an exact decimal number in plain notation: an optional
// minus sign, digits, and optionally a point and more digits. The zero
// value "" is 0. A literal converts directly (Price: "12.50"); text from
// outside goes through Parse.
type Decimal string

// Parse reads a decimal number: "12.50", "-3", "+0.5", or with an
// exponent, "1.25e3". The result is in plain notation with the sign
// normalized and redundant leading zeros removed; trailing zeros stay,
// since they carry the scale ("12.50").
func Parse(s string) (Decimal, error) {
	neg, digits, exp, ok := split(s)
	if !ok {
		return "", fmt.Errorf("decimal: %q is not a decimal number", s)
	}
	return plain(neg, digits, exp), nil
}

// MustParse is Parse for literals known to be valid; it panics otherwise.
func MustParse(s string) Decimal {
	d, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return d
}

// String returns the number; the zero value is "0".
func (d Decimal) String() string {
	if d == "" {
		return "0"
	}
	return string(d)
}

// Valid reports whether d is a decimal number (the zero value is).
func (d Decimal) Valid() bool {
	if d == "" {
		return true
	}
	_, _, _, ok := split(string(d))
	return ok
}

// Rat returns the exact value, or nil when d is not a number.
func (d Decimal) Rat() *big.Rat {
	if !d.Valid() {
		return nil
	}
	r, ok := new(big.Rat).SetString(d.String())
	if !ok {
		return nil
	}
	return r
}

// Cmp compares d and o exactly: -1 when d < o, 0 when equal, +1 when
// d > o. A value that is not a number compares as 0.
func (d Decimal) Cmp(o Decimal) int {
	a, b := d.Rat(), o.Rat()
	if a == nil {
		a = new(big.Rat)
	}
	if b == nil {
		b = new(big.Rat)
	}
	return a.Cmp(b)
}

// Sign returns -1, 0 or +1.
func (d Decimal) Sign() int { return d.Cmp("0") }

// FromRat renders r with scale digits after the point, rounding half away
// from zero as Postgres does.
func FromRat(r *big.Rat, scale int) Decimal {
	if scale < 0 {
		scale = 0
	}
	pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	num := new(big.Int).Mul(r.Num(), pow)
	q, m := new(big.Int).QuoRem(num, r.Denom(), new(big.Int))
	// Round half away from zero: |2m| >= denom.
	if m.Sign() != 0 && new(big.Int).Abs(new(big.Int).Lsh(m, 1)).Cmp(r.Denom()) >= 0 {
		if r.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	neg := q.Sign() < 0
	return plain(neg, new(big.Int).Abs(q).String(), -scale)
}

// Fits reports whether d is a number a numeric(precision, scale) column
// holds without rounding: at most precision-scale digits before the
// point and scale after it (trailing zeros aside).
func (d Decimal) Fits(precision, scale int) bool {
	_, digits, exp, ok := split(d.String())
	if !ok {
		return false
	}
	// digits × 10^exp; drop trailing zeros of the fraction.
	for exp < 0 && strings.HasSuffix(digits, "0") {
		digits, exp = digits[:len(digits)-1], exp+1
	}
	digits = strings.TrimLeft(digits, "0")
	frac := 0
	if exp < 0 {
		frac = -exp
	}
	whole := len(digits) + exp
	if whole < 0 {
		whole = 0
	}
	return frac <= scale && whole <= precision-scale
}

// MarshalJSON writes the number as a JSON string, "12.50".
func (d Decimal) MarshalJSON() ([]byte, error) {
	if !d.Valid() {
		return nil, fmt.Errorf("decimal: %q is not a decimal number", string(d))
	}
	return []byte(`"` + d.String() + `"`), nil
}

// UnmarshalJSON reads a JSON string ("12.50") or a JSON number (12.5,
// kept exactly as written).
func (d *Decimal) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" {
		return nil
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	v, err := Parse(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// MarshalText and UnmarshalText let a Decimal be a map key or a query
// parameter.
func (d Decimal) MarshalText() ([]byte, error) {
	if !d.Valid() {
		return nil, fmt.Errorf("decimal: %q is not a decimal number", string(d))
	}
	return []byte(d.String()), nil
}

func (d *Decimal) UnmarshalText(b []byte) error {
	v, err := Parse(string(b))
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// ScanNumeric reads a numeric column through pgx.
func (d *Decimal) ScanNumeric(n pgtype.Numeric) error {
	if !n.Valid {
		return errors.New("decimal: cannot scan NULL into decimal.Decimal; use *decimal.Decimal")
	}
	if n.NaN || n.InfinityModifier != pgtype.Finite {
		return errors.New("decimal: NaN and infinity are not decimal numbers")
	}
	v, err := n.Value()
	if err != nil {
		return err
	}
	*d = Decimal(v.(string))
	return nil
}

// NumericValue writes d to a numeric column through pgx.
func (d Decimal) NumericValue() (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if !d.Valid() {
		return n, fmt.Errorf("decimal: %q is not a decimal number", string(d))
	}
	err := n.ScanScientific(d.String())
	return n, err
}

// Scan reads a numeric column through database/sql.
func (d *Decimal) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		return errors.New("decimal: cannot scan NULL into decimal.Decimal; use *decimal.Decimal")
	case string:
		return d.UnmarshalText([]byte(v))
	case []byte:
		return d.UnmarshalText(v)
	case int64:
		*d = Decimal(fmt.Sprint(v))
		return nil
	}
	return fmt.Errorf("decimal: cannot scan %T", src)
}

// Value writes d through database/sql, as text.
func (d Decimal) Value() (driver.Value, error) {
	if !d.Valid() {
		return nil, fmt.Errorf("decimal: %q is not a decimal number", string(d))
	}
	return d.String(), nil
}

// split parses [+-]digits[.digits][(e|E)[+-]digits] into the sign, the
// digits without the point and the power of ten they are scaled by.
func split(s string) (neg bool, digits string, exp int, ok bool) {
	if s == "" {
		return false, "", 0, false
	}
	switch s[0] {
	case '-':
		neg, s = true, s[1:]
	case '+':
		s = s[1:]
	}
	mant, e, hasExp := strings.Cut(s, "e")
	if !hasExp {
		mant, e, hasExp = strings.Cut(s, "E")
	}
	whole, frac, hasPoint := strings.Cut(mant, ".")
	if whole == "" || (hasPoint && frac == "") || !allDigits(whole) || !allDigits(frac) {
		return false, "", 0, false
	}
	if hasExp {
		n, ok := atoi(e)
		if !ok {
			return false, "", 0, false
		}
		exp = n
	}
	return neg, whole + frac, exp - len(frac), true
}

// plain renders digits × 10^exp in plain notation.
func plain(neg bool, digits string, exp int) Decimal {
	var s string
	switch {
	case exp >= 0:
		s = digits + strings.Repeat("0", exp)
	case len(digits) > -exp:
		s = digits[:len(digits)+exp] + "." + digits[len(digits)+exp:]
	default:
		s = "0." + strings.Repeat("0", -exp-len(digits)) + digits
	}
	whole, frac, hasPoint := strings.Cut(s, ".")
	whole = strings.TrimLeft(whole, "0")
	if whole == "" {
		whole = "0"
	}
	s = whole
	if hasPoint {
		s += "." + frac
	}
	if neg && strings.Trim(s, "0.") != "" {
		s = "-" + s
	}
	return Decimal(s)
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// atoi reads a small signed exponent; exponents beyond ±10000 are
// refused rather than expanded.
func atoi(s string) (int, bool) {
	neg := false
	if s != "" && (s[0] == '-' || s[0] == '+') {
		neg, s = s[0] == '-', s[1:]
	}
	if s == "" || len(s) > 5 || !allDigits(s) {
		return 0, false
	}
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	if n > 10000 {
		return 0, false
	}
	if neg {
		n = -n
	}
	return n, true
}
