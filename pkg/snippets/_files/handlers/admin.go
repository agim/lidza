package handlers

import (
	"errors"
	"net/http"

	"github.com/agim/lidza/packs/admin"
	"github.com/agim/lidza/packs/db"

	"notes/db/queries/gen"
)

// AdminNotes is the app's own admin page: the newest notes across every
// account, and a delete action for moderation. It renders
// admin/notes.html inside the admin frame; the admin pack's gate (the
// first account and ADMIN_USERS) protects it like the built-in pages.
func AdminNotes() admin.Page {
	return admin.Page{
		Name: "Notes", Path: "notes", Icon: "file-text", Template: "notes.html",
		Data: func(r *http.Request) (any, error) {
			ctx := r.Context()
			q := queries.New(db.From(ctx))
			rows, err := q.AdminRecentNotes(ctx, 50)
			if err != nil {
				return nil, err
			}
			total, err := q.AdminCountNotes(ctx)
			if err != nil {
				return nil, err
			}
			return map[string]any{"Rows": rows, "Total": total}, nil
		},
		Actions: map[string]admin.Action{
			// delete removes a note and its attachment; the page shows the
			// message, or the error as an alert.
			"delete": func(r *http.Request) (string, error) {
				ctx := r.Context()
				id := r.Form.Get("id")
				if !uuidRe.MatchString(id) {
					return "", errors.New("no such note")
				}
				n, err := queries.New(db.From(ctx)).AdminDeleteNote(ctx, id)
				if err != nil {
					return "", err
				}
				if n == 0 {
					return "", errors.New("no such note")
				}
				if err := deleteAttachment(ctx, id); err != nil {
					return "", err
				}
				return "note deleted", nil
			},
		},
	}
}
