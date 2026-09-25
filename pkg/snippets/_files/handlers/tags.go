// Tag suggestions from a language model through the llm pack: the
// handler reads the note the way every note route does (the signed-in
// user's own), asks the model for a NoteTags document shaped by the
// schema type, and returns it validated. The provider is .env's
// LLM_PROVIDER; tests script the fake.
package handlers

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/packs/llm"
	"github.com/agim/lidza/pkg/router"

	"notes/db/queries/gen"
	"notes/schema"
)

// tagPrompt is the whole instruction; the note is the user message.
const tagPrompt = "Suggest one to five short lowercase topic tags for the note. Reply with the tags only."

func suggestTags(ctx context.Context, req *router.Request[router.None]) (schema.NoteTags, error) {
	row, err := queries.New(db.From(ctx)).GetNote(ctx, queries.GetNoteParams{ID: req.Param("id"), OwnerID: auth.CurrentUser(ctx).ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return schema.NoteTags{}, router.NotFound("note")
	}
	if err != nil {
		return schema.NoteTags{}, err
	}
	text := row.Title
	if row.Body != nil {
		text += "\n\n" + *row.Body
	}
	return llm.Generate[schema.NoteTags](ctx, llm.From(ctx), llm.Request{
		System:   tagPrompt,
		Messages: []llm.Message{{Role: llm.User, Content: text}},
	})
}
