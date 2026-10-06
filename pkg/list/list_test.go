package list

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza/pkg/router"
	"github.com/agim/lidza/pkg/validate"
)

func read(t *testing.T, query string, o Options) (Params, error) {
	t.Helper()
	req := &router.Request[router.None]{Raw: httptest.NewRequest("GET", "/api/v1/posts?"+query, nil)}
	return Read(req, o)
}

func TestRead(t *testing.T) {
	o := Options{Sorts: []string{"createdAt", "title"}, Desc: true, Filters: []string{"status"}}
	p, err := read(t, "", o)
	if err != nil || p.Limit != 50 || p.Offset != 0 || p.Sort != "createdAt" || !p.Desc || p.Search() != nil || p.Filter("status") != nil {
		t.Fatalf("defaults: %+v %v", p, err)
	}
	p, err = read(t, "limit=500&offset=100&q=+hello+&since=2026-10-01&until=2026-10-06T22:00:00Z&sort=title&order=asc&status=draft&other=x", o)
	if err != nil {
		t.Fatal(err)
	}
	if p.Limit != 200 || p.Offset != 100 || *p.Search() != "hello" || p.Sort != "title" || p.Desc || *p.Filter("status") != "draft" || p.Filters["other"] != "" {
		t.Fatalf("read: %+v", p)
	}
	if !p.Since.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) || !p.Until.Equal(time.Date(2026, 10, 6, 22, 0, 0, 0, time.UTC)) {
		t.Fatalf("range: %v %v", p.Since, p.Until)
	}
	_, err = read(t, "limit=0&offset=-1&sort=password&order=up&since=yesterday&q="+strings.Repeat("x", 201), o)
	var ve *validate.Errors
	if !errors.As(err, &ve) {
		t.Fatalf("not a validation error: %v", err)
	}
	var fields []string
	for _, f := range ve.Fields {
		fields = append(fields, f.Field)
	}
	if strings.Join(fields, ",") != "limit,offset,q,since,sort,order" {
		t.Fatalf("fields: %v", fields)
	}
	if _, err := read(t, "since=2026-10-06&until=2026-10-01", o); err == nil || !strings.Contains(err.Error(), "until") {
		t.Fatalf("backwards range: %v", err)
	}
	// No sorts allowed: none read.
	if p, _ := read(t, "sort=title", Options{}); p.Sort != "" {
		t.Fatalf("sort without Sorts: %q", p.Sort)
	}
}
