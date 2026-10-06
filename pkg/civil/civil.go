// Package civil holds dates and times as people write them, with no time
// zone: Date is a calendar day (a schema.lidza date field, a Postgres
// date column), DateTime a wall-clock time as a form's date-time input
// sends it (a schema.lidza localtime field). JSON carries them as written,
// "2026-10-06" and "2026-10-06T10:30", so no client shifts a day across
// a time zone on the way.
//
// A DateTime becomes an instant only in a zone: In(loc) resolves it,
// including the hour a daylight-saving change skips or repeats. The
// i18n pack does it in the request's zone (i18n.From(ctx).At).
package civil

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// Date is a calendar day. The zero value is no date: it encodes as JSON
// null and as SQL NULL.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// dateLayout is how a Date is written: ISO 8601, the HTML date input's.
const dateLayout = "2006-01-02"

// ParseDate reads "2026-10-06".
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return Date{}, fmt.Errorf("civil: %q is not a date (YYYY-MM-DD)", s)
	}
	return DateOf(t), nil
}

// DateOf is the calendar day of t, as t's own zone reads it: the day of
// an instant depends on the zone, so pass t.In(loc) for a person's day.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{y, m, d}
}

// Today is today's date in loc.
func Today(now time.Time, loc *time.Location) Date { return DateOf(now.In(loc)) }

// IsZero reports whether d is no date.
func (d Date) IsZero() bool { return d == Date{} }

// String is "2026-10-06"; "" for no date.
func (d Date) String() string {
	if d.IsZero() {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}

// In is the start of the day in loc (00:00 there, or the first instant
// of the day when a daylight-saving change skips midnight).
func (d Date) In(loc *time.Location) time.Time {
	t, _ := DateTime{Date: d}.In(loc)
	return t
}

// AddDays is the date n days later (earlier for a negative n).
func (d Date) AddDays(n int) Date {
	return DateOf(time.Date(d.Year, d.Month, d.Day+n, 12, 0, 0, 0, time.UTC))
}

// Before, After and Compare order dates.
func (d Date) Before(o Date) bool { return d.Compare(o) < 0 }
func (d Date) After(o Date) bool  { return d.Compare(o) > 0 }
func (d Date) Compare(o Date) int {
	switch {
	case d.Year != o.Year:
		return cmp(d.Year, o.Year)
	case d.Month != o.Month:
		return cmp(int(d.Month), int(o.Month))
	}
	return cmp(d.Day, o.Day)
}

// DaysUntil is how many days from d to o (negative when o is earlier).
func (d Date) DaysUntil(o Date) int {
	a := time.Date(d.Year, d.Month, d.Day, 12, 0, 0, 0, time.UTC)
	b := time.Date(o.Year, o.Month, o.Day, 12, 0, 0, 0, time.UTC)
	return int(b.Sub(a).Round(24*time.Hour) / (24 * time.Hour))
}

func cmp(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// MarshalJSON writes "2026-10-06", or null for no date.
func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + d.String() + `"`), nil
}

// UnmarshalJSON reads "2026-10-06" or null. A full timestamp
// ("2026-10-06T00:00:00Z", what releases before this type wrote) is read
// by its date as written, never shifted to another zone.
func (d *Date) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" {
		*d = Date{}
		return nil
	}
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return fmt.Errorf("civil: date %s is not a string", s)
	}
	s = s[1 : len(s)-1]
	if len(s) > len(dateLayout) && s[len(dateLayout)] == 'T' {
		s = s[:len(dateLayout)]
	}
	v, err := ParseDate(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// MarshalText and UnmarshalText serve query strings and form values.
func (d Date) MarshalText() ([]byte, error) { return []byte(d.String()), nil }
func (d *Date) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		*d = Date{}
		return nil
	}
	v, err := ParseDate(string(b))
	if err == nil {
		*d = v
	}
	return err
}

// ScanDate and DateValue let pgx read and write a date column.
func (d *Date) ScanDate(v pgtype.Date) error {
	if !v.Valid {
		*d = Date{}
		return nil
	}
	if v.InfinityModifier != pgtype.Finite {
		return errors.New("civil: an infinite date has no calendar day")
	}
	*d = DateOf(v.Time)
	return nil
}

func (d Date) DateValue() (pgtype.Date, error) {
	if d.IsZero() {
		return pgtype.Date{}, nil
	}
	return pgtype.Date{Time: time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC), Valid: true}, nil
}

// Scan and Value serve database/sql.
func (d *Date) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*d = Date{}
		return nil
	case time.Time:
		*d = DateOf(v)
		return nil
	case string:
		return d.UnmarshalText([]byte(v))
	case []byte:
		return d.UnmarshalText(v)
	}
	return fmt.Errorf("civil: cannot scan %T into a date", src)
}

func (d Date) Value() (driver.Value, error) {
	if d.IsZero() {
		return nil, nil
	}
	return d.String(), nil
}

// DateTime is a wall-clock time with no zone: what a person typed into a
// date-time input. The zero value is none.
type DateTime struct {
	Date
	Hour, Minute, Second int
}

// ParseDateTime reads "2026-10-06T10:30" or "2026-10-06T10:30:15" (a
// space instead of the T works too). A zone or offset is refused: such a
// value is an instant, a time field's.
func ParseDateTime(s string) (DateTime, error) {
	s = strings.Replace(s, " ", "T", 1)
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			h, m, sec := t.Clock()
			return DateTime{Date: DateOf(t), Hour: h, Minute: m, Second: sec}, nil
		}
	}
	return DateTime{}, fmt.Errorf("civil: %q is not a local date and time (YYYY-MM-DDTHH:MM, no zone)", s)
}

// IsZero reports whether dt is none.
func (dt DateTime) IsZero() bool { return dt == DateTime{} }

// String is "2026-10-06T10:30", with ":15" when the seconds are set.
func (dt DateTime) String() string {
	if dt.IsZero() {
		return ""
	}
	s := fmt.Sprintf("%sT%02d:%02d", dt.Date, dt.Hour, dt.Minute)
	if dt.Second != 0 {
		s += fmt.Sprintf(":%02d", dt.Second)
	}
	return s
}

// In is the instant dt names in loc. A daylight-saving change makes two
// cases: a time the clocks skip (02:30 when they jump from 02:00 to
// 03:00) is the moment the same distance past the change, 03:30, as most
// calendars do; a time they pass twice (01:30 when they fall back) is the
// first of the two. Skipped reports the first case so a form can say so.
func (dt DateTime) In(loc *time.Location) (t time.Time, skipped bool) {
	wall := time.Date(dt.Year, dt.Month, dt.Day, dt.Hour, dt.Minute, dt.Second, 0, time.UTC)
	// The offsets in force around the wall time: before and after any
	// change that day.
	var best time.Time
	for _, probe := range []time.Duration{-24 * time.Hour, 24 * time.Hour} {
		_, off := wall.Add(probe).In(loc).Zone()
		cand := wall.Add(-time.Duration(off) * time.Second)
		if sameWall(cand.In(loc), dt) && (best.IsZero() || cand.Before(best)) {
			best = cand
		}
	}
	if !best.IsZero() {
		return best, false
	}
	// Skipped: read the wall time with the offset in force before the
	// change, which lands after it.
	_, off := wall.Add(-24 * time.Hour).In(loc).Zone()
	return wall.Add(-time.Duration(off) * time.Second), true
}

func sameWall(t time.Time, dt DateTime) bool {
	y, m, d := t.Date()
	h, mi, s := t.Clock()
	return y == dt.Year && m == dt.Month && d == dt.Day && h == dt.Hour && mi == dt.Minute && s == dt.Second
}

// MarshalJSON writes "2026-10-06T10:30", or null for none.
func (dt DateTime) MarshalJSON() ([]byte, error) {
	if dt.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + dt.String() + `"`), nil
}

// UnmarshalJSON reads "2026-10-06T10:30[:15]" or null.
func (dt *DateTime) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" {
		*dt = DateTime{}
		return nil
	}
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return fmt.Errorf("civil: local date and time %s is not a string", s)
	}
	v, err := ParseDateTime(s[1 : len(s)-1])
	if err != nil {
		return err
	}
	*dt = v
	return nil
}

// MarshalText and UnmarshalText serve query strings and form values.
func (dt DateTime) MarshalText() ([]byte, error) { return []byte(dt.String()), nil }
func (dt *DateTime) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		*dt = DateTime{}
		return nil
	}
	v, err := ParseDateTime(string(b))
	if err == nil {
		*dt = v
	}
	return err
}
