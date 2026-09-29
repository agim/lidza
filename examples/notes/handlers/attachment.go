// One attachment per note through the storage pack. The upload is typed
// with router.File (the body is the file, not JSON), so the client has
// api.uploadAttachment({ id }, file); it sits behind the same rule as the
// note: the signed-in user's own. The object key follows from the note
// id, so no column is needed; reading streams the object through the
// app after the same check, and deleting the note deletes the object.
package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"

	"github.com/jackc/pgx/v5"

	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/packs/storage"
	"github.com/agim/lidza/pkg/router"

	"notes/db/queries/gen"
)

// maxAttachment bounds one upload.
const maxAttachment = 10 << 20

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// attachmentKey is where a note's attachment lives.
func attachmentKey(noteID string) string { return "notes/" + noteID + "/attachment" }

// AttachmentRoutes registers the upload and the download on the notes
// group (behind auth.Require()).
func AttachmentRoutes(r *router.Router) {
	router.Route(r, "PUT /api/v1/notes/{id}/attachment", uploadAttachment, router.UploadLimit(maxAttachment))
	r.HandleFunc("GET /api/v1/notes/{id}/attachment", downloadAttachment)
}

// ownNote loads the note when it is the signed-in user's; a 404 otherwise.
func ownNote(ctx context.Context, id string) (queries.Note, error) {
	if !uuidRe.MatchString(id) {
		return queries.Note{}, router.NotFound("note")
	}
	row, err := queries.New(db.From(ctx)).GetNote(ctx, queries.GetNoteParams{ID: id, OwnerID: auth.CurrentUser(ctx).ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return queries.Note{}, router.NotFound("note")
	}
	return row, err
}

// uploadAttachment stores the body as the note's attachment, replacing
// one already there. A body over maxAttachment replies 413.
func uploadAttachment(ctx context.Context, req *router.Request[router.File]) (router.None, error) {
	note, err := ownNote(ctx, req.Param("id"))
	if err != nil {
		return router.None{}, err
	}
	_, err = storage.From(ctx).Put(ctx, attachmentKey(note.ID), req.Body.Body, storage.PutOptions{ContentType: req.Body.ContentType})
	var tooLarge *http.MaxBytesError
	if err != nil && !errors.As(err, &tooLarge) {
		return router.None{}, router.Errorf(http.StatusBadRequest, "upload failed: %v", err)
	}
	return router.None{}, err
}

func downloadAttachment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	note, err := ownNote(ctx, r.PathValue("id"))
	if err != nil {
		router.WriteError(w, r, err)
		return
	}
	rc, obj, err := storage.From(ctx).Get(ctx, attachmentKey(note.ID))
	if errors.Is(err, storage.ErrNotFound) {
		router.WriteError(w, r, router.NotFound("attachment"))
		return
	}
	if err != nil {
		router.WriteError(w, r, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", obj.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	io.Copy(w, rc)
}

// deleteAttachment removes a note's object; called when the note goes.
func deleteAttachment(ctx context.Context, noteID string) error {
	return storage.From(ctx).Delete(ctx, attachmentKey(noteID))
}
