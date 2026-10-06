package civil

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestDateJSON(t *testing.T) {
	var v struct {
		Day  Date  `json:"day"`
		Opt  *Date `json:"opt"`
		None Date  `json:"none"`
	}
	if err := json.Unmarshal([]byte(`{"day":"2026-10-06","opt":null,"none":null}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.Day != (Date{2026, time.October, 6}) || v.Opt != nil || !v.None.IsZero() {
		t.Fatalf("decoded %+v", v)
	}
	out, _ := json.Marshal(v)
	if string(out) != `{"day":"2026-10-06","opt":null,"none":null}` {
		t.Fatalf("encoded %s", out)
	}
	// What earlier releases wrote: the date as written, never shifted.
	var old Date
	if err := json.Unmarshal([]byte(`"2026-10-06T00:00:00Z"`), &old); err != nil || old.String() != "2026-10-06" {
		t.Fatalf("timestamp: %v %v", old, err)
	}
	for _, bad := range []string{`"2026-13-01"`, `"06/10/2026"`, `20261006`, `"2026-02-30"`} {
		var d Date
		if err := json.Unmarshal([]byte(bad), &d); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestDateArithmetic(t *testing.T) {
	d := Date{2026, time.March, 28}
	if got := d.AddDays(5).String(); got != "2026-04-02" {
		t.Fatal(got)
	}
	// Across Europe's daylight-saving change: still whole days.
	if n := d.DaysUntil(Date{2026, time.April, 2}); n != 5 {
		t.Fatal(n)
	}
	if !d.Before(Date{2026, time.March, 29}) || d.After(d) || d.Compare(d) != 0 {
		t.Fatal("order")
	}
	ny, _ := time.LoadLocation("America/New_York")
	// 03:00 UTC on the 7th is still the 6th in New York.
	if got := Today(time.Date(2026, 10, 7, 3, 0, 0, 0, time.UTC), ny); got.String() != "2026-10-06" {
		t.Fatal(got)
	}
	if got := (Date{2026, time.October, 6}).In(ny); !got.Equal(time.Date(2026, 10, 6, 4, 0, 0, 0, time.UTC)) {
		t.Fatal(got)
	}
}

func TestDatePgx(t *testing.T) {
	var d Date
	if err := d.ScanDate(pgtype.Date{Time: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), Valid: true}); err != nil || d.String() != "2026-10-06" {
		t.Fatal(d, err)
	}
	v, _ := d.DateValue()
	if !v.Valid || v.Time.Format("2006-01-02") != "2026-10-06" {
		t.Fatal(v)
	}
	if v, _ := (Date{}).DateValue(); v.Valid {
		t.Fatal("zero date is not NULL")
	}
}

func TestDateTimeIn(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	at := func(s string) (time.Time, bool) {
		dt, err := ParseDateTime(s)
		if err != nil {
			t.Fatal(err)
		}
		return dt.In(ny)
	}
	// An ordinary time.
	if got, skipped := at("2026-10-06T10:30"); skipped || !got.Equal(time.Date(2026, 10, 6, 14, 30, 0, 0, time.UTC)) {
		t.Fatal(got, skipped)
	}
	// Spring forward, 2026-03-08 02:00 → 03:00: 02:30 does not exist and
	// becomes 03:30 EDT (07:30 UTC).
	if got, skipped := at("2026-03-08T02:30"); !skipped || !got.Equal(time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC)) {
		t.Fatal(got, skipped)
	}
	// Fall back, 2026-11-01 02:00 → 01:00: 01:30 happens twice; the first
	// (EDT, 05:30 UTC).
	if got, skipped := at("2026-11-01T01:30"); skipped || !got.Equal(time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)) {
		t.Fatal(got, skipped)
	}
	for _, bad := range []string{"2026-10-06T10:30Z", "2026-10-06T10:30+02:00", "2026-10-06", "10:30"} {
		if _, err := ParseDateTime(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	var dt DateTime
	if err := json.Unmarshal([]byte(`"2026-10-06T10:30"`), &dt); err != nil || dt.String() != "2026-10-06T10:30" {
		t.Fatal(dt, err)
	}
	if out, _ := json.Marshal(DateTime{Date: Date{2026, 10, 6}, Hour: 9, Minute: 5, Second: 7}); string(out) != `"2026-10-06T09:05:07"` {
		t.Fatal(string(out))
	}
}

// pgx reads and writes a date column as a Date, NULL as the zero Date.
func TestDatePostgres(t *testing.T) {
	dsn := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	defer conn.Close(ctx)
	var got, null Date
	if err := conn.QueryRow(ctx, `SELECT $1::date, NULL::date`, Date{2026, time.October, 6}).Scan(&got, &null); err != nil {
		t.Fatal(err)
	}
	if got.String() != "2026-10-06" || !null.IsZero() {
		t.Fatal(got, null)
	}
	var stored string
	if err := conn.QueryRow(ctx, `SELECT ($1::date)::text`, Date{2026, time.February, 28}).Scan(&stored); err != nil || stored != "2026-02-28" {
		t.Fatal(stored, err)
	}
}
