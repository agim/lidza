package admin

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Page is an admin page the app adds: an entry in the sidebar under the
// app's heading, served at Options.Path plus Page.Path behind the same
// admin gate, and rendered inside the pack's frame, so it has the
// sidebar, the light and dark themes, Tabler's classes, the icons and
// the template functions (icon, since, bytes, num, dict, initials) of
// the built-in pages.
//
//	admin.Mount(r, admin.Options{
//		Templates: lidza.Sub(adminFS, "admin"), // //go:embed admin
//		Pages: []admin.Page{{
//			Name: "Orders", Path: "orders", Icon: "inbox", Template: "orders.html",
//			Data: func(r *http.Request) (any, error) { return queries.New(db.From(r.Context())).RecentOrders(r.Context()) },
//			Actions: map[string]admin.Action{"refund": refundOrder},
//		}},
//	})
type Page struct {
	// Name is the sidebar entry and the page's title ("Orders").
	Name string
	// Path is the page's address under the admin pages ("orders" serves
	// /admin/orders); it may not be one of the built-in pages'.
	Path string
	// Icon is one of the vendored Tabler icons (assets/icons.txt in the
	// admin pack); "layout-dashboard" when empty.
	Icon string
	// Template names the page's file in Options.Templates (else in
	// Options.Dir on disk). It defines "content" and reads .Data.
	Template string
	// Data loads what the page shows, from the request's context (db.From,
	// the packs). An error is shown on the page instead of the content.
	Data func(r *http.Request) (any, error)
	// Actions are the page's forms: a POST to <Path>/<name> runs
	// Actions[name] and redirects back to the page with its message, or
	// with its error as an alert. A form that posts the frame's .Back as
	// "back" (<input type="hidden" name="back" value="{{$.Back}}">)
	// returns to the view it was sent from: the same page with the same
	// query (?order=1001), never another address.
	Actions map[string]Action
	// MaxUpload, when above zero, lets the page's forms send files
	// (enctype="multipart/form-data"): the body is limited to MaxUpload
	// bytes and an action reads r.MultipartForm.File; a larger body is
	// refused with an alert. Zero refuses file forms.
	MaxUpload int64
	// Downloads answer GET <Path>/<name> behind the same gate as the
	// page: an attachment, an export. The handler writes the whole
	// response; set Content-Type, Content-Disposition and
	// X-Content-Type-Options: nosniff for anything a user uploaded.
	Downloads map[string]http.HandlerFunc
}

// Action handles one of a page's forms. It reads r.Form (parsed) and the
// request's context, and returns the message the page shows next.
type Action func(r *http.Request) (string, error)

// builtinPaths are the addresses the pack's own pages use.
var builtinPaths = map[string]bool{
	"": true, "theme.css": true, "assets": true, "admins": true, "users": true, "settings": true, "credentials": true,
	"llm": true, "mail": true, "jobs": true, "storage": true,
}

// mountPages registers the app's pages; a clash with a built-in page or
// another app page is a programming error and panics at Mount.
func (h *Handler) mountPages() {
	seen := map[string]bool{}
	for _, pg := range h.opt.Pages {
		pg := pg
		path := strings.Trim(pg.Path, "/")
		if path == "" || strings.Contains(path, "/") || builtinPaths[path] || seen[path] {
			panic(fmt.Sprintf("admin: page %q: path %q is empty, nested, taken by a built-in page or used twice", pg.Name, pg.Path))
		}
		if pg.Template == "" || pg.Name == "" {
			panic(fmt.Sprintf("admin: page %q needs a Name and a Template", pg.Path))
		}
		seen[path] = true
		h.mux.HandleFunc("GET "+h.path+"/"+path, func(w http.ResponseWriter, r *http.Request) { h.appPage(w, r, pg) })
		h.mux.HandleFunc("POST "+h.path+"/"+path+"/{action}", func(w http.ResponseWriter, r *http.Request) { h.appAction(w, r, pg) })
		for name, fn := range pg.Downloads {
			if name == "" || strings.ContainsAny(name, "/{}? ") || fn == nil {
				panic(fmt.Sprintf("admin: page %q: download %q needs a plain name and a handler", pg.Name, name))
			}
			h.mux.HandleFunc("GET "+h.path+"/"+path+"/"+name, h.audited(fn))
		}
	}
}

// templatesFS is where the app's page templates are read.
func (h *Handler) templatesFS() fs.FS {
	if h.opt.Templates != nil {
		return h.opt.Templates
	}
	return os.DirFS(h.opt.Dir)
}

func (h *Handler) appPage(w http.ResponseWriter, r *http.Request, pg Page) {
	var data any
	var loadErr error
	if pg.Data != nil {
		data, loadErr = pg.Data(r)
	}
	src, err := fs.ReadFile(h.templatesFS(), pg.Template)
	if err != nil {
		http.Error(w, "admin: page "+pg.Name+": template: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderWith(w, r, http.StatusOK, pg.Name, pg.Name, appPageData{Data: data, Err: loadErr}, func(t *template.Template) (*template.Template, error) {
		return t.New(pg.Template).Parse(string(src))
	})
}

// appPageData wraps what Page.Data returned, so the template's .Data is
// the app's value and a load error still renders.
type appPageData struct {
	Data any
	Err  error
}

func (h *Handler) appAction(w http.ResponseWriter, r *http.Request, pg Page) {
	back := "/" + strings.Trim(pg.Path, "/")
	action := pg.Actions[r.PathValue("action")]
	if action == nil {
		http.NotFound(w, r)
		return
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt == "multipart/form-data" {
		if pg.MaxUpload <= 0 {
			h.redirect(w, r, back, "", "this form takes no files")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, pg.MaxUpload)
		if err := r.ParseMultipartForm(min(pg.MaxUpload, 8<<20)); err != nil {
			msg := "bad form"
			if errors.As(err, new(*http.MaxBytesError)) {
				msg = "the upload is larger than " + humanBytes(pg.MaxUpload)
			}
			h.redirect(w, r, back, "", msg)
			return
		}
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	} else if err := r.ParseForm(); err != nil {
		h.redirect(w, r, back, "", "bad form")
		return
	}
	back += viewQuery(r.Form.Get("back"))
	msg, err := action(r)
	if err != nil {
		h.redirect(w, r, back, "", err.Error())
		return
	}
	if msg == "" {
		msg = "done"
	}
	h.redirect(w, r, back, msg, "")
}

// viewQuery is a page's query for returning to the same view ("?a=1"),
// without the flash and alert of an earlier action; anything but a query
// (another path, a scheme, an over-long value) is dropped.
func viewQuery(raw string) string {
	if raw == "" || len(raw) > 2048 || raw[0] != '?' {
		return ""
	}
	v, err := url.ParseQuery(raw[1:])
	if err != nil {
		return ""
	}
	v.Del("saved")
	v.Del("error")
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}

// appNav lists the app's pages for the sidebar.
func (h *Handler) appNav(_ context.Context) []navItem {
	var out []navItem
	for _, pg := range h.opt.Pages {
		icon := pg.Icon
		if icon == "" {
			icon = "layout-dashboard"
		}
		out = append(out, navItem{Name: pg.Name, Href: h.path + "/" + strings.Trim(pg.Path, "/"), Icon: icon})
	}
	return out
}
