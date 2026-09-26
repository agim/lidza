package brief

import (
	"strings"

	"github.com/agim/lidza/pkg/recipes"
)

// Recipes the brief seeds in the guide's App recipes section. Each is
// written once, from the first answer that calls for it; after that it is
// the app's, edited like any other recipe.
const (
	RecipeScope = "Scope a query in this app"
	RecipeStyle = "Style a page to match the app"
	RecipeData  = "Import or seed data"
)

// seedRecipe writes the recipe an answer calls for when the guide lacks
// it; it returns the recipe's name, or "".
func seedRecipe(dir, id, answer string) (string, error) {
	var title, description string
	var steps []string
	switch id {
	case "ownership":
		title = RecipeScope
		description, steps = scopeRecipe(answer)
	case "palette":
		title = RecipeStyle
		description = "Build a page that looks like the rest of this app: its palette, type, mood and themes, from the brief's Design section, never a stock look."
		steps = []string{
			"Read the Design section of `" + File + "` (palette, typography, mood, themes, languages); the tokens are in `src/index.css` (`bg-brand`, `text-ink`, `bg-surface`, `border-line`).",
			"Use the tokens, never raw colours or a stock blue; status colours (green, yellow, red) only for status.",
			"Every list gets a loading skeleton, an empty state with an action and a keyboard path; motion stays under 300 ms and respects `prefers-reduced-motion`.",
			"Strings go through the i18n pack when the brief names more than one language.",
			"Check both themes when the brief asks for light and dark, and a phone width.",
			"`lidza check` (accessibility is enforced), then `lidza test --e2e`.",
		}
	case "content":
		lower := strings.ToLower(answer)
		if !strings.Contains(lower, "dataset") && !strings.Contains(lower, "import") && !strings.Contains(lower, "seed") {
			return "", nil
		}
		title = RecipeData
		description = "Bring content into the app from the source the brief names: resumable, idempotent, attributed, and never from the network in tests."
		steps = []string{
			"Record the source, its licence and its attribution in `docs/decisions.md` (`lidza decision add`).",
			"Write the import as a job (`jobs.FromServices(s).Handle` in `start.go`): batches, a rate limit the source allows, a descriptive User-Agent, upserts keyed on the source's own id so a rerun changes nothing.",
			"Store files through the storage pack (never hotlink) and derived images through the media pack.",
			"Show progress and failures on an app admin page with a retry action (recipe \"Extend the admin pages\").",
			"Commit a small fixture (twenty to thirty records, small files) and test the import against it; tests never reach the network.",
			"`lidza check`, then `lidza test`.",
		}
	default:
		return "", nil
	}
	existing, err := recipes.Load(dir)
	if err != nil {
		return "", nil // no guide yet: nothing to seed into
	}
	for _, r := range existing {
		if r.Name == recipes.Slug(title) {
			return "", nil
		}
	}
	r, err := recipes.Add(dir, title, description, steps)
	if err != nil {
		return "", err
	}
	if _, err := recipes.Sync(dir); err != nil {
		return "", err
	}
	return r.Name, nil
}

// scopeRecipe specializes the framework's "Scope a query to the
// signed-in user" to the ownership model the brief names.
func scopeRecipe(answer string) (string, []string) {
	lower := strings.ToLower(answer)
	test := "Test it: another user lists nothing and gets 404 on the first user's id; `lidza check`, then `lidza test`."
	switch {
	case strings.Contains(lower, "each user owns"):
		return "Every row belongs to one user and only they see it: the brief's ownership model (" + File + ").",
			[]string{
				"Give the model an `ownerId uuid @index` and set it from `auth.CurrentUser(ctx).ID` on create, never from the body.",
				"Add `AND owner_id = $N` to every query in `db/queries/<table>.sql`: list, count, get, update, delete.",
				"Reply `router.NotFound` for another user's row, never 403.",
				test,
			}
	case strings.Contains(lower, "invited members") || strings.Contains(lower, "shared"):
		return "Rows belong to a shared parent (a project, a board) whose members see them: the brief's ownership model (" + File + ").",
			[]string{
				"Keep one membership table: `Membership { parentId @ref(Parent, cascade), userId, role }` with `@@unique(parentId, userId)`.",
				"Join every query on it (`JOIN membership m ON m.parent_id = t.parent_id AND m.user_id = $N`), and check the parent first in one helper, `requireMember(ctx, parentID, role)` in `handlers/access.go`: 404 when not a member, 403 when the role may read but not do this.",
				"Invitations add memberships through an emailed, expiring token (recipe \"Send an email\").",
				"Publish changes on a topic per parent, authorized to its members (recipe \"Publish live updates\").",
				test,
			}
	case strings.Contains(lower, "organization") && !strings.Contains(lower, "one organization"):
		return "Everything belongs to an organization; members act by role: the brief's ownership model (" + File + ").",
			[]string{
				"Every model gets `orgId uuid @index`; `Membership { orgId, userId, role }` with `@@unique(orgId, userId)`.",
				"The current organization comes from the path (`/api/v1/orgs/{org}/...`) and is checked in one middleware on the group: membership, then role.",
				"Every query filters on `org_id`; a row of another organization is a 404.",
				"Roles are the brief's (Data, \"Which roles are there?\"); the checks live in `handlers/access.go`.",
				test,
			}
	case strings.Contains(lower, "public"):
		return "Anyone reads; authors change their own: the brief's ownership model (" + File + ").",
			[]string{
				"Reads are public routes (or behind `auth.Optional()` to mark the viewer's own rows).",
				"Writes are behind `auth.Require()`; update and delete add `AND author_id = $N`.",
				"Moderation (hide, delete any) is an admin page (recipe \"Extend the admin pages\").",
				test,
			}
	}
	return "The brief's ownership model (" + File + "): " + oneLine(answer) + ".",
		[]string{
			"Read the Data section of `" + File + "` and the framework recipe \"Scope a query to the signed-in user\".",
			"Put the access rule in one helper in `handlers/access.go` and call it from every handler; filter every query in `db/queries` by it.",
			test,
		}
}
