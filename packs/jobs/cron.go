package jobs

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Cron is a Schedule from a five-field cron expression ("minute hour
// day-of-month month day-of-week") read in a time zone ("" is UTC):
//
//	jobs.Cron("0 9 * * 1-5", "Europe/Tirane")   // weekdays at 09:00 local
//	jobs.Cron("*/15 * * * *", "")                // every quarter hour
//	jobs.Cron("@monthly", "UTC")                 // the 1st at 00:00
//
// Each field is *, a number, a name (jan-dec, sun-sat), a range (1-5), a
// step (*/15, 1-30/5) or a list of those (1,15); day-of-week counts
// Sunday as 0 or 7. When both the day-of-month and the day-of-week are
// restricted, a day matching either is due, as cron has always read it.
// The aliases @hourly, @daily (@midnight), @weekly, @monthly and
// @yearly (@annually) stand for their expressions; @reboot has no
// meaning for a schedule every node shares and is refused.
//
// Times follow the zone's clock as Daily does: a wall-clock time the
// spring change skips runs at the instant the clocks jump to, and one
// the autumn change repeats runs once. An expression that is never due
// ("0 0 30 2 *") is refused, as are a bad field and an unknown zone.
func Cron(expression, zone string) (Schedule, error) {
	c := &cron{text: strings.Join(strings.Fields(expression), " "), zone: zone, loc: time.UTC}
	if zone != "" {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			return nil, fmt.Errorf("jobs: cron time zone %q: %w", zone, err)
		}
		c.loc = loc
	}
	expr := c.text
	if alias, ok := cronAliases[expr]; ok {
		expr = alias
	} else if strings.HasPrefix(expr, "@") {
		if expr == "@reboot" {
			return nil, errors.New("jobs: cron @reboot: a schedule is shared by every node and runs on the clock, not at a process start; use Every, Daily or an expression")
		}
		return nil, fmt.Errorf("jobs: cron %q: unknown alias (one of @hourly, @daily, @midnight, @weekly, @monthly, @yearly, @annually)", expr)
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("jobs: cron %q: want five fields (minute hour day-of-month month day-of-week), got %d", c.text, len(fields))
	}
	if set, err := cronField(fields[0], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("jobs: cron %q: minute: %w", c.text, err)
	} else {
		copy(c.minutes[:], set)
	}
	if set, err := cronField(fields[1], 0, 23, nil); err != nil {
		return nil, fmt.Errorf("jobs: cron %q: hour: %w", c.text, err)
	} else {
		copy(c.hours[:], set)
	}
	c.anyDom = fields[2] == "*"
	if set, err := cronField(fields[2], 1, 31, nil); err != nil {
		return nil, fmt.Errorf("jobs: cron %q: day of month: %w", c.text, err)
	} else {
		copy(c.days[:], set)
	}
	if set, err := cronField(fields[3], 1, 12, monthNames); err != nil {
		return nil, fmt.Errorf("jobs: cron %q: month: %w", c.text, err)
	} else {
		copy(c.months[:], set)
	}
	c.anyDow = fields[4] == "*"
	if set, err := cronField(fields[4], 0, 7, dayNames); err != nil {
		return nil, fmt.Errorf("jobs: cron %q: day of week: %w", c.text, err)
	} else {
		copy(c.weekdays[:], set)
	}
	if c.weekdays[7] {
		c.weekdays[0] = true // Sunday either way
	}
	if c.Next(time.Now()).IsZero() {
		return nil, fmt.Errorf("jobs: cron %q is never due (no such date in the next years)", c.text)
	}
	return c, nil
}

var cronAliases = map[string]string{
	"@hourly":   "0 * * * *",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@weekly":   "0 0 * * 0",
	"@monthly":  "0 0 1 * *",
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
}

var monthNames = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
var dayNames = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

// cron is a parsed expression: one set per field, as arrays indexed by
// value.
type cron struct {
	text, zone string
	loc        *time.Location
	minutes    [60]bool
	hours      [24]bool
	days       [32]bool
	months     [13]bool
	weekdays   [8]bool
	// anyDom and anyDow record a "*", which decides how the two day
	// fields combine.
	anyDom, anyDow bool
}

// cronField reads one field into a set over 0..hi (true at the values
// it names); names map words to values.
func cronField(field string, lo, hi int, names map[string]int) ([]bool, error) {
	out := make([]bool, hi+1)
	if field == "" {
		return nil, errors.New("empty")
	}
	for _, part := range strings.Split(field, ",") {
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n < 1 {
				return nil, fmt.Errorf("step %q", part)
			}
			step, part = n, part[:i]
		}
		from, to := lo, hi
		switch {
		case part == "*":
		case strings.Contains(part, "-"):
			a, b, _ := strings.Cut(part, "-")
			var err error
			if from, err = cronValue(a, lo, hi, names); err != nil {
				return nil, err
			}
			if to, err = cronValue(b, lo, hi, names); err != nil {
				return nil, err
			}
			if from > to {
				return nil, fmt.Errorf("range %q runs backwards", part)
			}
		default:
			v, err := cronValue(part, lo, hi, names)
			if err != nil {
				return nil, err
			}
			from, to = v, v
			if step > 1 {
				to = hi // "5/10" is 5, 15, 25, ...
			}
		}
		for v := from; v <= to; v += step {
			out[v] = true
		}
	}
	return out, nil
}

func cronValue(s string, lo, hi int, names map[string]int) (int, error) {
	if v, ok := names[strings.ToLower(s)]; ok {
		return v, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("value %q", s)
	}
	if v < lo || v > hi {
		return 0, fmt.Errorf("value %d outside %d-%d", v, lo, hi)
	}
	return v, nil
}

// dayDue applies cron's day rule to a date.
func (c *cron) dayDue(d time.Time) bool {
	if !c.months[int(d.Month())] {
		return false
	}
	dom, dow := c.days[d.Day()], c.weekdays[int(d.Weekday())]
	switch {
	case c.anyDom && c.anyDow:
		return true
	case c.anyDom:
		return dow
	case c.anyDow:
		return dom
	default:
		return dom || dow
	}
}

// Next is the earliest due instant after t: the first date on or after
// t's that the day fields admit, and on it the earliest of its wall-clock
// times that falls after t, placed on the date by time.Date so the zone's
// clock changes are met as Daily meets them. Nothing within five years
// is never.
func (c *cron) Next(t time.Time) time.Time {
	lt := t.In(c.loc)
	var hours, minutes []int
	for h, on := range c.hours {
		if on {
			hours = append(hours, h)
		}
	}
	for m, on := range c.minutes {
		if on {
			minutes = append(minutes, m)
		}
	}
	for i := 0; i <= 366*5; i++ {
		noon := time.Date(lt.Year(), lt.Month(), lt.Day()+i, 12, 0, 0, 0, c.loc)
		if !c.dayDue(noon) {
			continue
		}
		// After the first day every time of the day is after t; on the
		// first, only those past it.
		var best time.Time
		for _, h := range hours {
			for _, m := range minutes {
				at := time.Date(noon.Year(), noon.Month(), noon.Day(), h, m, 0, 0, c.loc)
				if at.After(t) && (best.IsZero() || at.Before(best)) {
					best = at
				}
			}
			if !best.IsZero() && i > 0 {
				break
			}
		}
		if !best.IsZero() {
			return best
		}
	}
	return time.Time{}
}

func (c *cron) String() string {
	zone := c.zone
	if zone == "" {
		zone = "UTC"
	}
	return "cron " + c.text + " " + zone
}

// sortedInts is for tests and the admin page: the values a field holds.
func sortedInts(set []bool) []int {
	var out []int
	for v, on := range set {
		if on {
			out = append(out, v)
		}
	}
	sort.Ints(out)
	return out
}
