package jobs

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCronParse(t *testing.T) {
	for expr, want := range map[string]string{
		"0 9 * * 1-5":       "cron 0 9 * * 1-5 Europe/Tirane",
		"  */15  *  * * * ": "cron */15 * * * * Europe/Tirane",
		"@hourly":           "cron @hourly Europe/Tirane",
	} {
		s, err := Cron(expr, "Europe/Tirane")
		if err != nil || s.String() != want {
			t.Errorf("%q: %v %q", expr, err, s)
		}
	}
	if s, err := Cron("@daily", ""); err != nil || s.String() != "cron @daily UTC" {
		t.Errorf("utc: %v %v", s, err)
	}
	for _, c := range []struct{ expr, zone, want string }{
		{"0 9 * *", "", "five fields"},
		{"60 * * * *", "", "outside 0-59"},
		{"0 25 * * *", "", "outside 0-23"},
		{"0 0 0 * *", "", "outside 1-31"},
		{"0 0 * 13 *", "", "outside 1-12"},
		{"0 0 * * 8", "", "outside 0-7"},
		{"5-1 * * * *", "", "runs backwards"},
		{"*/0 * * * *", "", "step"},
		{"a * * * *", "", `value "a"`},
		{"@reboot", "", "@reboot"},
		{"@fortnightly", "", "unknown alias"},
		{"0 0 30 2 *", "", "never due"},
		{"0 0 * * *", "Mars/Olympus", "time zone"},
	} {
		_, err := Cron(c.expr, c.zone)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q %q: %v, want %q", c.expr, c.zone, err, c.want)
		}
	}
	// Names, lists, ranges and steps, and Sunday as 0 or 7.
	s, err := Cron("0 6 1,15 jan-mar,dec mon,SUN", "")
	if err != nil {
		t.Fatal(err)
	}
	c := s.(*cron)
	if got := fmt.Sprint(sortedInts(c.months[:])); got != "[1 2 3 12]" {
		t.Errorf("months %v", got)
	}
	if !c.weekdays[0] || !c.weekdays[1] || c.weekdays[2] {
		t.Errorf("weekdays %v", c.weekdays)
	}
	if s7, _ := Cron("0 0 * * 7", ""); !s7.(*cron).weekdays[0] {
		t.Error("7 is not Sunday")
	}
	if st, _ := Cron("5/10 * * * *", ""); fmt.Sprint(sortedInts(st.(*cron).minutes[:])) != "[5 15 25 35 45 55]" {
		t.Errorf("step from a value: %v", sortedInts(st.(*cron).minutes[:]))
	}
}

func TestCronNext(t *testing.T) {
	utc := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	next := func(expr, zone, from string) string {
		s, err := Cron(expr, zone)
		if err != nil {
			t.Fatal(err)
		}
		return s.Next(utc(from)).UTC().Format(time.RFC3339)
	}
	for _, c := range []struct{ expr, zone, from, want string }{
		{"*/15 * * * *", "", "2026-11-03T10:07:00Z", "2026-11-03T10:15:00Z"},
		{"*/15 * * * *", "", "2026-11-03T10:15:00Z", "2026-11-03T10:30:00Z"},             // strictly after
		{"0 9 * * 1-5", "Europe/Tirane", "2026-11-06T09:00:00Z", "2026-11-09T08:00:00Z"}, // Friday 10:00 local → Monday 09:00 (+01:00)
		{"0 9 * * 1-5", "Europe/Tirane", "2026-07-03T12:00:00Z", "2026-07-06T07:00:00Z"}, // summer, +02:00
		{"0 0 1 * 1", "", "2026-11-03T00:00:00Z", "2026-11-09T00:00:00Z"},                // the 1st or a Monday: Monday first
		{"0 0 1 * 1", "", "2026-11-28T00:00:00Z", "2026-11-30T00:00:00Z"},                // Monday the 30th before Tuesday the 1st
		{"0 0 31 * *", "", "2026-11-03T00:00:00Z", "2026-12-31T00:00:00Z"},               // November has no 31st
		{"0 0 29 2 *", "", "2026-03-01T00:00:00Z", "2028-02-29T00:00:00Z"},               // the next leap year
		{"@monthly", "", "2026-11-03T00:00:00Z", "2026-12-01T00:00:00Z"},
		{"@weekly", "", "2026-11-03T00:00:00Z", "2026-11-08T00:00:00Z"},
		{"30 2 * * *", "Europe/Tirane", "2027-03-27T12:00:00Z", "2027-03-28T01:30:00Z"}, // the skipped 02:30 runs at 03:30 (+02:00)
		{"30 2 * * *", "Europe/Tirane", "2027-03-28T01:30:00Z", "2027-03-29T00:30:00Z"}, // then back to 02:30 local
		{"30 2 * * *", "Europe/Tirane", "2026-10-24T12:00:00Z", "2026-10-25T01:30:00Z"}, // the repeated 02:30 runs once, as Daily places it (+01:00)
		{"30 2 * * *", "Europe/Tirane", "2026-10-25T00:30:00Z", "2026-10-25T01:30:00Z"}, // from the first occurrence: that one run
		{"30 2 * * *", "Europe/Tirane", "2026-10-25T01:30:00Z", "2026-10-26T01:30:00Z"}, // not again
	} {
		if got := next(c.expr, c.zone, c.from); got != c.want {
			t.Errorf("%q %q from %s: %s, want %s", c.expr, c.zone, c.from, got, c.want)
		}
	}
	// Strictly increasing, and one run per due time, over a year across
	// both clock changes.
	for _, expr := range []string{"*/20 * * * *", "30 2 * * *", "0 9 * * 1-5", "0 0 L * *"} {
		s, err := Cron(expr, "Europe/Tirane")
		if expr == "0 0 L * *" {
			if err == nil {
				t.Error("L accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		at := utc("2026-01-01T00:00:00Z")
		for i := 0; i < 400; i++ {
			n := s.Next(at)
			if !n.After(at) {
				t.Fatalf("%s: %s after %s is not later", expr, n, at)
			}
			at = n
		}
	}
	// A declared cron schedule passes Schedule's validation like the others.
	var sched Schedule
	sched, err := Cron("0 9 * * 1-5", "Europe/Tirane")
	if err != nil || sched.Next(time.Now()).IsZero() {
		t.Fatal(err)
	}
}
