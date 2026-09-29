package jobs

import (
	"testing"
	"time"
)

// TestDailyAt: several times a day in a zone, in order, across both
// clock changes.
func TestDailyAt(t *testing.T) {
	tirane := mustLoc(t, "Europe/Tirane")
	utc := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	three := DailyAt("Europe/Tirane", "18:00", "02:00", "10:00")
	for s, want := range map[Schedule]string{
		three:                "daily 02:00,10:00,18:00 Europe/Tirane",
		DailyAt("", "07:05"): Daily("07:05", "").String(),
		DailyAt("Europe/Tirane", "10:00", "10:00"): "daily 10:00 Europe/Tirane",
	} {
		if s.String() != want {
			t.Fatalf("spec %q, want %q", s.String(), want)
		}
	}
	for _, c := range [][2]string{
		{"2026-09-27T09:00:00Z", "2026-09-27T16:00:00Z"}, // 11:00 +02:00: 18:00
		{"2026-09-27T16:00:00Z", "2026-09-28T00:00:00Z"}, // strictly after: 02:00 tomorrow
		{"2026-09-28T00:00:00Z", "2026-09-28T08:00:00Z"},
		{"2026-10-26T00:30:00Z", "2026-10-26T01:00:00Z"}, // winter, +01:00
		{"2026-10-26T01:00:00Z", "2026-10-26T09:00:00Z"},
		{"2027-03-29T12:00:00Z", "2027-03-29T16:00:00Z"}, // summer again, +02:00
	} {
		if got := three.Next(utc(c[0])); !got.Equal(utc(c[1])) {
			t.Fatalf("%s from %s: %s, want %s", three, c[0], got.UTC().Format(time.RFC3339), c[1])
		}
	}

	// Four days over each change: three runs a local day at the
	// wall-clock times, the skipped 02:30 moved to 03:30, the repeated
	// one run once.
	night := DailyAt("Europe/Tirane", "02:30", "10:00", "18:00")
	for _, from := range []time.Time{
		time.Date(2027, 3, 26, 20, 0, 0, 0, tirane),  // clocks jump 02:00 to 03:00 on the 28th
		time.Date(2026, 10, 23, 20, 0, 0, 0, tirane), // back 03:00 to 02:00 on the 25th
	} {
		at, days := from, map[string]int{}
		for range 12 {
			next := night.Next(at)
			if !next.After(at) {
				t.Fatalf("from %s: %s is not later", at, next)
			}
			at = next
			l := at.In(tirane)
			days[l.Format("2006-01-02")]++
			clock := l.Format("15:04")
			if l.Day() == 28 && l.Month() == time.March && l.Hour() < 4 {
				if clock != "03:30" {
					t.Fatalf("skipped 02:30 ran at %s, want 03:30", l)
				}
			} else if clock != "02:30" && clock != "10:00" && clock != "18:00" {
				t.Fatalf("run at %s", l)
			}
		}
		if len(days) != 4 {
			t.Fatalf("runs by day from %s: %v", from, days)
		}
		for day, n := range days {
			if n != 3 {
				t.Fatalf("runs by day from %s: %s has %d", from, day, n)
			}
		}
	}

	// Two times the jump puts on one instant run once that day.
	merged := DailyAt("Europe/Tirane", "02:30", "03:30")
	first := merged.Next(time.Date(2027, 3, 28, 0, 0, 0, 0, tirane))
	second := merged.Next(first)
	if l := first.In(tirane); l.Format("15:04") != "03:30" || second.In(tirane).Day() != 29 {
		t.Fatalf("merged times on the jump: %s then %s", l, second.In(tirane))
	}

	q := New(Config{}, nil)
	for _, s := range []Schedule{DailyAt("Europe/Tirane"), DailyAt("", "10:00", "9am"), DailyAt("Mars/Olympus", "10:00")} {
		if err := q.Schedule("x", s, nil); err == nil {
			t.Fatalf("%s accepted", s)
		}
	}
}
