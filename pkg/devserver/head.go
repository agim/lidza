package devserver

import (
	"bytes"
	"encoding/json"
	"html"
	"net/http"
	"regexp"
	"strings"
)

// Head is what a page's <head> says about it, decided on the server per
// request: the title, the description, the canonical address, the social
// cards (Open Graph and Twitter) and structured data (JSON-LD). A page
// with a path parameter is one shell for every address until the head
// hook (WithHead) names it; search engines and link previews read this,
// not what the browser renders later.
//
// Every value is text: it is escaped where it is written, so a title
// taken from a database row cannot close a tag.
type Head struct {
	// Title replaces the page's <title>; og:title and twitter:title
	// follow it.
	Title string
	// Description replaces the page's meta description; og:description
	// and twitter:description follow it.
	Description string
	// Canonical is the page's absolute address: <link rel="canonical">
	// and og:url.
	Canonical string
	// Image is an absolute image address for og:image and twitter:image;
	// with one, twitter:card is summary_large_image, else summary.
	Image string
	// Type is og:type; "website" when empty.
	Type string
	// SiteName is og:site_name.
	SiteName string
	// NoIndex asks search engines not to index the page (robots
	// noindex): a draft, a private page.
	NoIndex bool
	// Meta are further <meta> tags (og:locale, article:published_time,
	// twitter:site); each replaces a tag of the page with the same name
	// or property.
	Meta []Meta
	// JSONLD are structured data objects (a map, a struct, a
	// json.RawMessage), each written as its own
	// <script type="application/ld+json">.
	JSONLD []any
	// Status is the response status when not zero: 404 for an address
	// whose record does not exist, so a crawler does not index an empty
	// page.
	Status int
}

// Meta is one <meta> tag: Name (name="...") or Property
// (property="...", the Open Graph form), and Content.
type Meta struct {
	Name     string
	Property string
	Content  string
}

// HeadFunc decides the head of the page r asks for; false leaves the
// page as built. It runs for every page request (not for assets), with
// the request's context carrying the app's services, so it may read the
// database; keep it to one query.
type HeadFunc func(r *http.Request) (Head, bool)

// WithHead sets the head of every page Static, the sidecar and the dev
// proxy serve with f: the prerendered pages and the shell alike.
func WithHead(f HeadFunc) Option {
	return func(o *options) { o.head = f }
}

var (
	// The tags the head replaces, with the rest of their line, so no
	// blank lines are left behind.
	titleRe     = regexp.MustCompile(`(?is)<title\b[^>]*>.*?</title\s*>[ \t]*(?:\r?\n)?`)
	metaRe      = regexp.MustCompile(`(?is)<meta\b[^>]*>[ \t]*(?:\r?\n)?`)
	canonicalRe = regexp.MustCompile(`(?is)<link\b[^>]*\brel\s*=\s*["']?canonical\b[^>]*>[ \t]*(?:\r?\n)?`)
	attrRe      = regexp.MustCompile(`(?is)\b(name|property)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	headOpenRe  = regexp.MustCompile(`(?is)<head\b[^>]*>`)
	headCloseRe = regexp.MustCompile(`(?i)</head\s*>`)
)

// tags lists the head's meta tags in order, keyed by name or property.
func (h Head) tags() []Meta {
	var out []Meta
	add := func(m Meta) {
		if m.Content != "" {
			out = append(out, m)
		}
	}
	add(Meta{Name: "description", Content: h.Description})
	if h.NoIndex {
		add(Meta{Name: "robots", Content: "noindex"})
	}
	if h.Title != "" || h.Description != "" || h.Canonical != "" || h.Image != "" {
		typ := h.Type
		if typ == "" {
			typ = "website"
		}
		add(Meta{Property: "og:type", Content: typ})
		add(Meta{Property: "og:title", Content: h.Title})
		add(Meta{Property: "og:description", Content: h.Description})
		add(Meta{Property: "og:url", Content: h.Canonical})
		add(Meta{Property: "og:image", Content: h.Image})
		add(Meta{Property: "og:site_name", Content: h.SiteName})
		card := "summary"
		if h.Image != "" {
			card = "summary_large_image"
		}
		add(Meta{Name: "twitter:card", Content: card})
		add(Meta{Name: "twitter:title", Content: h.Title})
		add(Meta{Name: "twitter:description", Content: h.Description})
		add(Meta{Name: "twitter:image", Content: h.Image})
	}
	for _, m := range h.Meta {
		if m.Name != "" || m.Property != "" {
			out = append(out, m)
		}
	}
	return out
}

func metaKey(m Meta) string {
	if m.Property != "" {
		return "property:" + strings.ToLower(m.Property)
	}
	return "name:" + strings.ToLower(m.Name)
}

// InjectHead writes h into page's <head>: the title replaced, the meta
// tags it sets replacing the page's of the same name or property, the
// canonical link replaced, the JSON-LD added. A page without a <head> is
// returned as it is.
func InjectHead(page []byte, h Head) []byte {
	closeLoc := headCloseRe.FindIndex(page)
	if closeLoc == nil {
		return page
	}
	headStart := 0
	if open := headOpenRe.FindIndex(page[:closeLoc[0]]); open != nil {
		headStart = open[1]
	}
	head := page[headStart:closeLoc[0]]

	tags := h.tags()
	drop := map[string]bool{}
	for _, m := range tags {
		drop[metaKey(m)] = true
	}
	if h.Title != "" {
		head = titleRe.ReplaceAll(head, nil)
	}
	if h.Canonical != "" {
		head = canonicalRe.ReplaceAll(head, nil)
	}
	head = metaRe.ReplaceAllFunc(head, func(tag []byte) []byte {
		for _, a := range attrRe.FindAllSubmatch(tag, -1) {
			val := string(a[2]) + string(a[3]) + string(a[4])
			if drop[strings.ToLower(string(a[1]))+":"+strings.ToLower(html.UnescapeString(val))] {
				return nil
			}
		}
		return tag
	})

	var b bytes.Buffer
	if h.Title != "" {
		b.WriteString("<title>" + html.EscapeString(h.Title) + "</title>\n")
	}
	for _, m := range tags {
		if m.Property != "" {
			b.WriteString(`<meta property="` + html.EscapeString(m.Property))
		} else {
			b.WriteString(`<meta name="` + html.EscapeString(m.Name))
		}
		b.WriteString(`" content="` + html.EscapeString(m.Content) + "\">\n")
	}
	if h.Canonical != "" {
		b.WriteString(`<link rel="canonical" href="` + html.EscapeString(h.Canonical) + "\">\n")
	}
	for _, v := range h.JSONLD {
		data, err := json.Marshal(v)
		if err != nil || len(data) == 0 || string(data) == "null" {
			continue
		}
		// Marshal escapes <, > and & (and U+2028, U+2029) inside strings,
		// so no value can end the script; HTMLEscape covers a
		// json.RawMessage's own bytes the same way.
		var safe bytes.Buffer
		json.HTMLEscape(&safe, data)
		b.WriteString(`<script type="application/ld+json">`)
		b.Write(safe.Bytes())
		b.WriteString("</script>\n")
	}

	out := make([]byte, 0, len(page)+b.Len())
	out = append(out, page[:headStart]...)
	out = append(out, head...)
	out = append(out, b.Bytes()...)
	out = append(out, page[closeLoc[0]:]...)
	return out
}

// withHead applies the hook to a page about to be served; the status is
// 200 unless the head names another.
func withHead(f HeadFunc, r *http.Request, page []byte) ([]byte, int) {
	if f == nil {
		return page, http.StatusOK
	}
	h, ok := f(r)
	if !ok {
		return page, http.StatusOK
	}
	status := http.StatusOK
	if h.Status != 0 {
		status = h.Status
	}
	return InjectHead(page, h), status
}
