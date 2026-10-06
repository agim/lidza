package i18n

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"golang.org/x/text/language"

	"github.com/agim/lidza"
	"github.com/agim/lidza/pkg/civil"
)

type fixedClock time.Time

func (c fixedClock) Now() time.Time { return time.Time(c) }

// at is a context at a fixed time in a zone, as a request carries them.
func at(now time.Time, zone string) context.Context {
	s := lidza.NewServices()
	lidza.Provide[lidza.Clock](s, fixedClock(now))
	loc, err := time.LoadLocation(zone)
	if err != nil {
		panic(err)
	}
	return WithTimezone(lidza.WithServices(context.Background(), s), loc)
}

func TestAtAndToday(t *testing.T) {
	i, err := New(fstest.MapFS{"en.json": {Data: []byte(`{"hi":"Hi"}`)}}, "en")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 3, 0, 0, 0, time.UTC)
	ny := at(now, "America/New_York")
	// What a datetime-local input sent, read in the visitor's zone.
	dt, _ := civil.ParseDateTime("2026-10-06T10:30")
	if got, skipped := i.At(ny, dt); skipped || !got.Equal(time.Date(2026, 10, 6, 14, 30, 0, 0, time.UTC)) {
		t.Fatal(got, skipped)
	}
	tokyo := at(now, "Asia/Tokyo")
	if got, _ := i.At(tokyo, dt); !got.Equal(time.Date(2026, 10, 6, 1, 30, 0, 0, time.UTC)) {
		t.Fatal(got)
	}
	// 03:00 UTC on the 7th: still the 6th in New York, the 7th in Tokyo.
	if d := i.Today(ny); d.String() != "2026-10-06" {
		t.Fatal(d)
	}
	if d := i.Today(tokyo); d.String() != "2026-10-07" {
		t.Fatal(d)
	}
}

func TestRelative(t *testing.T) {
	en, err := New(fstest.MapFS{"en.json": {Data: []byte(`{"hi":"Hi"}`)}, "de.json": {Data: []byte(`{
		"_relative": {"past": "vor %s", "future": "in %s", "now": "gerade eben",
			"hour": {"one": "%d Stunde", "other": "%d Stunden"}}}`)}}, "en")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	ctx := at(now, "UTC")
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{-10 * time.Second, "just now"},
		{-3 * time.Hour, "3 hours ago"},
		{-61 * time.Minute, "1 hour ago"},
		{2*24*time.Hour + time.Hour, "in 2 days"},
		{-400 * 24 * time.Hour, "1 year ago"},
		{90 * time.Second, "in 1 minute"},
	} {
		if got := en.Relative(ctx, now.Add(c.d)); got != c.want {
			t.Errorf("%v: %q, want %q", c.d, got, c.want)
		}
	}
	de := WithLocale(ctx, language.German)
	if got := en.Relative(de, now.Add(-3*time.Hour)); got != "vor 3 Stunden" {
		t.Fatal(got)
	}
	if got := en.Relative(de, now); got != "gerade eben" {
		t.Fatal(got)
	}
}
