// Package list reads what a list route was asked for: a page (limit and
// offset, capped), a search, a time range, a sort among the allowed keys
// and equality filters, from the query string, validated, so a handler
// passes them to its query and nothing it did not allow reaches SQL.
//
//	p, err := list.Read(req, list.Options{
//		Sorts:   []string{"createdAt", "title"}, // the first is the default
//		Desc:    true,                           // newest first unless asked
//		Filters: []string{"status"},
//	})
//	if err != nil {
//		return out, err // 422 with the field at fault
//	}
//	rows, err := q.ListPosts(ctx, queries.ListPostsParams{
//		Q: p.Search(), Since: p.Since, Until: p.Until, Status: p.Filter("status"),
//		Sort: p.Sort, Desc: p.Desc, Lim: p.Limit, Off: p.Offset,
//	})
//
// The query string: limit, offset, q, since and until (an instant,
// "2026-10-06T00:00:00Z", or a day, "2026-10-06", read as midnight UTC;
// pages send instants from the visitor's zone), sort (one of Sorts),
// order ("asc" or "desc"), and one parameter per filter.
package list

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/agim/lidza/pkg/router"
	"github.com/agim/lidza/pkg/validate"
)

// Options say what a list route accepts.
type Options struct {
	// Limit is the page size when the query names none (50); MaxLimit
	// caps what it may ask for (200).
	Limit, MaxLimit int
	// Sorts are the keys the list may be sorted by; the first is the
	// default. Empty: no sort parameter is accepted.
	Sorts []string
	// Desc is the default order.
	Desc bool
	// Filters are the query parameters taken as equality filters.
	Filters []string
	// MaxSearch bounds the search text (200 characters).
	MaxSearch int
}

// Params are what the request asked for, validated.
type Params struct {
	Limit, Offset int32
	// Q is the search, trimmed; "" for none.
	Q string
	// Since and Until bound the time range, Until exclusive; nil for open.
	Since, Until *time.Time
	// Sort is one of Options.Sorts; Desc the order.
	Sort string
	Desc bool
	// Filters holds the filters the request set.
	Filters map[string]string
}

// Read validates the list parameters of req: an unknown sort, a bad
// number or time, a range that ends before it starts, or a search too
// long is a 422 naming the parameter (validate.Errors, as for a body).
func Read[In any](req *router.Request[In], o Options) (Params, error) {
	if o.Limit <= 0 {
		o.Limit = 50
	}
	if o.MaxLimit <= 0 {
		o.MaxLimit = 200
	}
	if o.MaxSearch <= 0 {
		o.MaxSearch = 200
	}
	var errs validate.Errors
	p := Params{Limit: int32(min(o.Limit, o.MaxLimit)), Filters: map[string]string{}}
	if v := req.Query("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		switch {
		case err != nil || n < 1:
			errs.Add("limit", "min", "a whole number of at least 1")
		default:
			p.Limit = int32(min(int(n), o.MaxLimit))
		}
	}
	if v := req.Query("offset"); v != "" {
		// At most 2^31-1, the int4 the query takes; larger wrapped to a
		// negative offset Postgres refuses (a 500).
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 0 {
			errs.Add("offset", "min", "a whole number from 0 to 2147483647")
		} else {
			p.Offset = int32(n)
		}
	}
	p.Q = strings.TrimSpace(req.Query("q"))
	if len([]rune(p.Q)) > o.MaxSearch {
		errs.Add("q", "max", "at most "+strconv.Itoa(o.MaxSearch)+" characters")
	}
	for _, b := range []struct {
		name string
		into **time.Time
	}{{"since", &p.Since}, {"until", &p.Until}} {
		v := req.Query(b.name)
		if v == "" {
			continue
		}
		t, err := parseTime(v)
		if err != nil {
			errs.Add(b.name, "time", "an instant (2026-10-06T00:00:00Z) or a day (2026-10-06)")
			continue
		}
		*b.into = &t
	}
	if p.Since != nil && p.Until != nil && !p.Until.After(*p.Since) {
		errs.Add("until", "range", "after since")
	}
	if len(o.Sorts) > 0 {
		p.Sort, p.Desc = o.Sorts[0], o.Desc
		if v := req.Query("sort"); v != "" {
			if !slices.Contains(o.Sorts, v) {
				errs.Add("sort", "enum", "one of "+strings.Join(o.Sorts, ", "))
			} else {
				p.Sort = v
			}
		}
	}
	switch req.Query("order") {
	case "":
	case "asc":
		p.Desc = false
	case "desc":
		p.Desc = true
	default:
		errs.Add("order", "enum", "asc or desc")
	}
	for _, f := range o.Filters {
		if v := req.Query(f); v != "" {
			if len(v) > 200 {
				errs.Add(f, "max", "at most 200 characters")
				continue
			}
			p.Filters[f] = v
		}
	}
	// A 422 with the fields, as a body that fails validation.
	return p, errs.Result()
}

func parseTime(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", v)
}

// Search is Q for a query's nullable parameter: nil for no search.
func (p Params) Search() *string {
	if p.Q == "" {
		return nil
	}
	return &p.Q
}

// Filter is a filter's value for a nullable parameter: nil when unset.
func (p Params) Filter(name string) *string {
	if v, ok := p.Filters[name]; ok {
		return &v
	}
	return nil
}

// Page is a page of a list: the items, the total that match, and the
// page asked for, so a pager knows where it is.
type Page[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// HighlightStart and HighlightStop mark the matched words in the
// headline the generated highlight queries return (ts_headline's
// StartSel and StopSel): control characters, never markup, so the text
// of a row stays text.
const (
	HighlightStart = "\x02"
	HighlightStop  = "\x03"
)

// Part is a piece of a highlighted text: the matched words have Hit.
type Part struct {
	Text string `json:"text"`
	Hit  bool   `json:"hit"`
}

// Split cuts a headline marked with HighlightStart and HighlightStop
// into parts; the page renders a hit in <mark>, escaped like any text.
func Split(headline string) []Part {
	var out []Part
	for headline != "" {
		start := strings.Index(headline, HighlightStart)
		if start < 0 {
			out = append(out, Part{Text: headline})
			break
		}
		if start > 0 {
			out = append(out, Part{Text: headline[:start]})
		}
		rest := headline[start+len(HighlightStart):]
		stop := strings.Index(rest, HighlightStop)
		if stop < 0 {
			if rest != "" {
				out = append(out, Part{Text: rest, Hit: true})
			}
			break
		}
		if stop > 0 {
			out = append(out, Part{Text: rest[:stop], Hit: true})
		}
		headline = rest[stop+len(HighlightStop):]
	}
	return out
}
