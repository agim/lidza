// Generated once by lidza gen resource Note, then scoped to the signed-in
// user: every query takes the owner id, so one user never sees another's
// notes. The routes are registered on a group behind auth.Require().

package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/pkg/router"

	"notes/db/queries/gen"
	"notes/schema"
)

// NoteRoutes registers the Note resource under /api/v1/notes.
func NoteRoutes(r *router.Router) {
	router.Route(r, "GET /api/v1/notes", listNotes)
	router.Route(r, "GET /api/v1/notes/{id}", getNote)
	router.Route(r, "POST /api/v1/notes", createNote)
	router.Route(r, "PATCH /api/v1/notes/{id}", updateNote)
	router.Route(r, "DELETE /api/v1/notes/{id}", deleteNote)
}

func listNotes(ctx context.Context, req *router.Request[router.None]) (schema.NoteList, error) {
	owner := auth.CurrentUser(ctx).ID
	limit, offset := PageParams(req, 50, 200)
	q := queries.New(db.From(ctx))
	rows, err := q.ListNotes(ctx, queries.ListNotesParams{OwnerID: owner, Limit: limit, Offset: offset})
	if err != nil {
		return schema.NoteList{}, err
	}
	total, err := q.CountNotes(ctx, owner)
	if err != nil {
		return schema.NoteList{}, err
	}
	items := make([]schema.Note, len(rows))
	for i, row := range rows {
		items[i] = toNote(row)
	}
	return schema.NoteList{Items: items, Total: int(total)}, nil
}

func getNote(ctx context.Context, req *router.Request[router.None]) (schema.Note, error) {
	row, err := queries.New(db.From(ctx)).GetNote(ctx, queries.GetNoteParams{ID: req.Param("id"), OwnerID: auth.CurrentUser(ctx).ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return schema.Note{}, router.NotFound("note")
	}
	if err != nil {
		return schema.Note{}, err
	}
	return toNote(row), nil
}

func createNote(ctx context.Context, req *router.Request[schema.CreateNote]) (schema.Note, error) {
	in := req.Body
	row, err := queries.New(db.From(ctx)).CreateNote(ctx, queries.CreateNoteParams{
		OwnerID: auth.CurrentUser(ctx).ID,
		Title:   in.Title,
		Body:    in.Body,
	})
	if err != nil {
		return schema.Note{}, err
	}
	req.Status(http.StatusCreated)
	return toNote(row), nil
}

func updateNote(ctx context.Context, req *router.Request[schema.UpdateNote]) (schema.Note, error) {
	in := req.Body
	row, err := queries.New(db.From(ctx)).UpdateNote(ctx, queries.UpdateNoteParams{
		ID:      req.Param("id"),
		OwnerID: auth.CurrentUser(ctx).ID,
		Title:   in.Title,
		Body:    in.Body,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return schema.Note{}, router.NotFound("note")
	}
	if err != nil {
		return schema.Note{}, err
	}
	return toNote(row), nil
}

func deleteNote(ctx context.Context, req *router.Request[router.None]) (router.None, error) {
	n, err := queries.New(db.From(ctx)).DeleteNote(ctx, queries.DeleteNoteParams{ID: req.Param("id"), OwnerID: auth.CurrentUser(ctx).ID})
	if err != nil {
		return router.None{}, err
	}
	if n == 0 {
		return router.None{}, router.NotFound("note")
	}
	return router.None{}, nil
}

// toNote maps a row to the API type.
func toNote(row queries.Note) schema.Note {
	return schema.Note{
		ID:        row.ID,
		OwnerID:   row.OwnerID,
		Title:     row.Title,
		Body:      row.Body,
		CreatedAt: row.CreatedAt,
	}
}
