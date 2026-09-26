package brief

// Kind is how a question is answered.
type Kind string

// Kinds: one of the suggestions, any number of them, or free text. Every
// kind also takes an answer of the developer's own.
const (
	One  Kind = "one"
	Many Kind = "many"
	Text Kind = "text"
)

// Suggestion is one proposed answer.
type Suggestion struct {
	Value  string `json:"value"`
	Detail string `json:"detail,omitempty"`
}

// Question is one question of the brief.
type Question struct {
	ID      string `json:"id"`
	Section string `json:"section"`
	Ask     string `json:"ask"`
	// Why says what the answer changes, so the developer knows why it is
	// asked and the agent knows where it applies.
	Why         string       `json:"why"`
	Kind        Kind         `json:"kind"`
	Suggestions []Suggestion `json:"suggestions,omitempty"`
	// Required questions are the ones an agent cannot build well without;
	// lidza check warns while they are open (L015).
	Required bool `json:"required,omitempty"`
	// Decision, when set, records the answer in docs/decisions.md under
	// this title.
	Decision string `json:"-"`
	// Agreement puts the answer in the agent files' Working agreements.
	Agreement string `json:"-"`
}

// Sections, in interview order.
var Sections = []string{"Product", "Data", "Accounts", "Design", "Content", "Services", "Deployment", "Working agreements"}

// Questions are the brief, in interview order.
var Questions = []Question{
	// Product.
	{ID: "purpose", Section: "Product", Kind: Text, Required: true,
		Ask: "What does the app do, in one sentence?",
		Why: "Every agent reads it first; it decides what a feature is for."},
	{ID: "users", Section: "Product", Kind: Many, Required: true,
		Ask: "Who uses it?",
		Why: "Sets the accounts, the tone and what a page shows first.",
		Suggestions: []Suggestion{
			{"Individuals, for themselves", ""}, {"Small teams working together", ""},
			{"Businesses and their customers", "two sides with different pages"},
			{"Visitors who only read", "public pages without an account"}, {"Our own staff", "an internal tool"},
		}},
	{ID: "journeys", Section: "Product", Kind: Text, Required: true,
		Ask: "What are the three to five things people come to do?",
		Why: "They become the first features, the browser tests and the landing page."},
	{ID: "out_of_scope", Section: "Product", Kind: Many,
		Ask:         "What is out of scope for now?",
		Why:         "Agents do not build it, and say so when a request needs it.",
		Suggestions: []Suggestion{{"Native mobile apps", ""}, {"Payments", ""}, {"Offline use", ""}, {"Public API for other developers", ""}, {"More than one language", ""}}},

	// Data.
	{ID: "ownership", Section: "Data", Kind: One, Required: true, Decision: "Brief: who owns the data",
		Ask: "Who owns the data, and who may see it?",
		Why: "Every query is scoped by it; the most expensive answer to change later.",
		Suggestions: []Suggestion{
			{"Each user owns their rows", "private; another user's row is a 404"},
			{"Shared with invited members", "projects, boards or lists with members and roles"},
			{"Organizations own everything", "teams; members by role; one account in several teams"},
			{"Public content with authors", "anyone reads, authors edit their own"},
			{"One organization, internal", "every signed-in user sees everything"},
		}},
	{ID: "roles", Section: "Data", Kind: One,
		Ask:         "Which roles are there?",
		Why:         "Decides the membership table and every permission check.",
		Suggestions: []Suggestion{{"Owner and member", ""}, {"Admin, editor and viewer", ""}, {"No roles: everyone who has access may do everything", ""}}},
	{ID: "personal_data", Section: "Data", Kind: Many, Decision: "Brief: personal data kept",
		Ask:         "Which personal data does it keep?",
		Why:         "Decides what is encrypted, exported, deleted, and what the privacy page says.",
		Suggestions: []Suggestion{{"Email addresses only", ""}, {"Names and profiles", ""}, {"Files people upload", ""}, {"Payment details, through a provider", "the app never stores card numbers"}, {"Regulated data", "health, finance, children: say which"}}},
	{ID: "retention", Section: "Data", Kind: One,
		Ask:         "What happens to an account's data when it is deleted?",
		Why:         "Becomes the delete-account feature and the jobs that purge.",
		Suggestions: []Suggestion{{"Deleted at once", ""}, {"Kept 30 days, then purged", "undo is possible"}, {"Kept for legal reasons", "say how long"}}},

	// Accounts.
	{ID: "signin", Section: "Accounts", Kind: Many, Required: true, Decision: "Brief: sign-in",
		Ask:         "How do people sign in?",
		Why:         "Configures auth.Mount and the sign-in page.",
		Suggestions: []Suggestion{{"Email and password", ""}, {"Google", ""}, {"GitHub", ""}, {"Microsoft", "work and school accounts"}, {"A company identity provider", "Okta, Keycloak, Auth0: OpenID Connect"}}},
	{ID: "registration", Section: "Accounts", Kind: One,
		Ask:         "Who may create an account?",
		Why:         "auth.Options NoRegister, invitations, or open sign-up.",
		Suggestions: []Suggestion{{"Anyone", "open registration"}, {"Only invited people", ""}, {"Only admins create accounts", ""}}},

	// Design.
	{ID: "palette", Section: "Design", Kind: One, Required: true, Decision: "Brief: palette",
		Ask:         "Which palette?",
		Why:         "Written into the design tokens (src/index.css) and the admin theme; agents never fall back to a stock blue.",
		Suggestions: paletteSuggestions()},
	{ID: "typography", Section: "Design", Kind: One,
		Ask:         "Which typography?",
		Why:         "Sets the fonts in the tokens and the page recipe.",
		Suggestions: []Suggestion{{"An editorial serif for headings, a clean sans for text", ""}, {"One clean sans throughout", ""}, {"A rounded, friendly sans", ""}, {"A sans with monospace accents", "technical tools"}}},
	{ID: "mood", Section: "Design", Kind: Many,
		Ask:         "What should it feel like?",
		Why:         "Spacing, density, motion and the tone of the copy.",
		Suggestions: []Suggestion{{"Calm and spacious", ""}, {"Dense and efficient", "many rows per screen"}, {"Playful", ""}, {"Premium", ""}, {"Plain and technical", ""}}},
	{ID: "themes", Section: "Design", Kind: One,
		Ask:         "Light and dark?",
		Why:         "Whether every page ships both themes.",
		Suggestions: []Suggestion{{"Light and dark, following the system, with a toggle", ""}, {"Light only", ""}, {"Dark only", ""}}},
	{ID: "languages", Section: "Design", Kind: Many,
		Ask:         "Which languages?",
		Why:         "The i18n pack's catalogs from the first page, or English strings only.",
		Suggestions: []Suggestion{{"English", ""}, {"Albanian", ""}, {"German", ""}, {"Italian", ""}, {"French", ""}, {"Spanish", ""}}},
	{ID: "locale", Section: "Design", Kind: Text,
		Ask:         "Which currency and time zone?",
		Why:         "Formatting of money and dates.",
		Suggestions: []Suggestion{{"EUR, Europe/Tirane", ""}, {"USD, America/New_York", ""}, {"The viewer's own", "from the browser"}}},

	// Content.
	{ID: "content", Section: "Content", Kind: One, Decision: "Brief: where the content comes from",
		Ask:         "Where does the content come from?",
		Why:         "Decides the import job, the seed data and the fixtures tests use.",
		Suggestions: []Suggestion{{"People create it in the app", ""}, {"Seed data we provide", ""}, {"A public dataset", "name it and its licence"}, {"An existing database to import", ""}}},

	// Services.
	{ID: "mail", Section: "Services", Kind: One, Decision: "Brief: mail provider",
		Ask:         "Which mail provider in production?",
		Why:         "The mail pack's settings; tests use the outbox either way.",
		Suggestions: []Suggestion{{"Resend", ""}, {"Postmark", ""}, {"Mailgun", ""}, {"An SMTP server", ""}, {"No mail", ""}}},
	{ID: "model", Section: "Services", Kind: One, Decision: "Brief: language model",
		Ask:         "Which language model provider?",
		Why:         "The llm pack's settings; embeddings need OpenAI, Gemini, Ollama or a compatible server.",
		Suggestions: []Suggestion{{"Anthropic", ""}, {"OpenAI", ""}, {"Google Gemini", ""}, {"A local or self-hosted server", "llama.cpp, vLLM, Ollama"}, {"No model", ""}}},
	{ID: "model_budget", Section: "Services", Kind: One,
		Ask:         "How much may the model cost a month?",
		Why:         "Agents cache, pick smaller models and label usage to stay under it.",
		Suggestions: []Suggestion{{"Under $20", ""}, {"Under $100", ""}, {"Under $500", ""}, {"No limit yet", ""}}},
	{ID: "storage", Section: "Services", Kind: One, Decision: "Brief: file storage",
		Ask:         "Where do files go in production?",
		Why:         "The storage pack's settings.",
		Suggestions: []Suggestion{{"Amazon S3", ""}, {"Cloudflare R2", ""}, {"Another S3-compatible service", ""}, {"No files", ""}}},
	{ID: "payments", Section: "Services", Kind: One, Decision: "Brief: payments",
		Ask:         "Payments?",
		Why:         "A provider's checkout and webhooks, keys in the credentials.",
		Suggestions: []Suggestion{{"Stripe", ""}, {"Paddle", "merchant of record"}, {"Not needed", ""}}},

	// Deployment.
	{ID: "hosting", Section: "Deployment", Kind: One, Decision: "Brief: hosting",
		Ask:         "Where will it run?",
		Why:         "lidza ship's output: the systemd unit or the image.",
		Suggestions: []Suggestion{{"One server with systemd", ""}, {"Docker on a server", ""}, {"A container platform", "Fly, Render, Kubernetes"}, {"Not decided", ""}}},
	{ID: "domain", Section: "Deployment", Kind: Text,
		Ask: "Which domain?",
		Why: "LIDZA_TLS_DOMAINS, the sign-in callbacks and the links in mail."},
	{ID: "scale", Section: "Deployment", Kind: One,
		Ask:         "How many people in the first year?",
		Why:         "How far the agents go with caching, paging and background work.",
		Suggestions: []Suggestion{{"Up to a hundred", ""}, {"Thousands", ""}, {"Tens of thousands or more", ""}}},
	{ID: "region", Section: "Deployment", Kind: One, Decision: "Brief: where data lives",
		Ask:         "Where must the data stay?",
		Why:         "The region of the database, storage and providers.",
		Suggestions: []Suggestion{{"In the EU", ""}, {"In the US", ""}, {"Anywhere", ""}}},

	// Working agreements.
	{ID: "push", Section: "Working agreements", Kind: One, Required: true, Agreement: "Pushing",
		Ask:         "When should an agent push?",
		Why:         "Unpushed work runs no CI and nobody sees it.",
		Suggestions: []Suggestion{{"After every verified commit", ""}, {"When a feature is done", ""}, {"Never: I push myself", ""}}},
	{ID: "approval", Section: "Working agreements", Kind: Many, Agreement: "Ask first",
		Ask:         "What must an agent ask before doing?",
		Why:         "Everything else it does and reports.",
		Suggestions: []Suggestion{{"Adding a dependency", ""}, {"Changing the schema", ""}, {"Deleting data or files", ""}, {"Anything outside the request", ""}, {"Nothing: go ahead and report", ""}}},
	{ID: "tests", Section: "Working agreements", Kind: One, Agreement: "Tests",
		Ask:         "What is the test bar?",
		Why:         "What a feature needs before it counts as done.",
		Suggestions: []Suggestion{{"A Go test for every handler and a browser test for every page", ""}, {"A Go test for every handler", ""}, {"The critical paths only", ""}}},
	{ID: "done", Section: "Working agreements", Kind: Text, Agreement: "Done means",
		Ask:         "What else does done mean?",
		Why:         "Beyond green tests: docs, screenshots, a demo, a changelog line.",
		Suggestions: []Suggestion{{"The README says how to use it", ""}, {"A line in the changelog", ""}, {"Screenshots in the report", ""}}},
	{ID: "reports", Section: "Working agreements", Kind: One, Agreement: "Reports",
		Ask:         "How should an agent report?",
		Why:         "The shape of the message at the end of a task.",
		Suggestions: []Suggestion{{"Short: the outcome and what is next", ""}, {"Per feature: what was built and how it is tested", ""}, {"Only when blocked or done", ""}}},
}

// Find returns a question by id.
func Find(id string) (Question, bool) {
	for _, q := range Questions {
		if q.ID == id {
			return q, true
		}
	}
	return Question{}, false
}
