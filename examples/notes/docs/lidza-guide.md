# Līdza guide for notes

Written by `lidza new`. One source of truth for every agent working here;
`CLAUDE.md`, `AGENTS.md` and `GEMINI.md` point at this file.

## What this is

A Līdza app is one Go binary. In production it serves the API under `/api`
and the built frontend for every other path. In development, `lidza dev`
runs the same binary with the frontend proxied from its dev server, so the
app is always at one address: http://127.0.0.1:3000.

## Layout

```
notes/
├── main.go          entrypoint: lidza.Run(...). Do not edit.
├── routes.go        API handlers. Add routes here (or in packages it calls).
├── schema.lidza     data shapes: models (tables), types (API shapes), enums
├── schema/          generated Go structs with Validate(). Do not edit.
├── db/              generated schema.sql, migrations/, schema.lock.json; queries/*.sql with the db pack
├── packs.go         generated from lidza.json "packs". Do not edit.
├── tools.go         the app's MCP tools (lidza.ToolFunc), served by lidza mcp and /mcp
├── packs/<name>/    a pack: pack.lidza.json, rust/ crate, generated pack.go and <name>.wasm
├── lidza.json       project config: name, frontend template, dev server, dist
├── go.mod           module notes, requires github.com/agim/lidza
├── package.json     frontend (react); npm scripts dev, build, check
├── src/             frontend source
│   ├── router.tsx   client-side routes
│   └── pages/       one file per page; API calls via `import { api } from '@lidza/client'`
├── .lidza/client/   generated TypeScript client (gitignored, `lidza gen`)
├── dist/            frontend build output, embedded into the binary. Never edit.
├── .githooks/pre-commit  runs lidza verify before every commit
├── .claude/skills/  one skill per recipe below (Claude Code), generated from this file
├── .agents/skills/  the same skills for Codex; .gemini/commands/lidza/ the same as Gemini commands
├── docs/lidza-guide.md   this file
└── .lidza/          dev build artifacts (gitignored)
```

## Commands

| Command | What it does |
|---|---|
| `lidza dev` | starts the frontend dev server (http://127.0.0.1:5173), builds and runs the app on http://127.0.0.1:3000, rebuilds on Go changes. `npm install` runs automatically when `node_modules` is missing. |
| `lidza build` | builds the frontend into `dist/` (routes without parameters prerendered to static HTML), then compiles `bin/notes`: one binary, no Node at runtime. |
| `lidza check --json` | regenerates, then runs `go vet`, `staticcheck`, `cargo check` and `tsc`; prints one JSON list of diagnostics, each with `layer`, `file`, `line`, `message`. Exit 1 while there are errors. A frontend call that no longer matches a handler fails here. |
| `lidza gen` | `schema.lidza` to `schema/schema.go`, `db/schema.sql`, a migration in `db/migrations` when models changed; handlers to `.lidza/openapi.json` and the client in `.lidza/client`. `lidza dev` runs it on every change. |
| `lidza context` | writes `.lidza/context.json`: routes, handler signatures, Rust exports. |
| `lidza mcp` | MCP server on stdio; see "Agent interface". |
| `lidza gen resource <Model>` | queries, `Create/Update/<Model>List` types, `handlers/<table>.go` with list, get, create, patch, delete under `/api/v1/<plural>`, registered in `routes.go`. Needs the `db` pack. |
| `lidza pack add <name>` | enables an official pack: `db`, `auth`, `jobs`, `cache`, `i18n`, `realtime`, `geo`, `media`. |
| `lidza test [go test flags]` | creates and migrates the `.env.test` database, runs `go test ./...` with `LIDZA_MODE=test`, then the frontend check. |
| `lidza test --e2e [--install]` | builds the app, starts the binary with `.env.test`, runs the Playwright suite in `e2e/`; `--install` fetches the browser when missing. |
| `lidza verify [--json] [--no-test]` | before a commit: regenerates and requires the generated files to be staged, runs the checks, runs the Go tests. The pre-commit hook in `.githooks/` runs it (`git commit --no-verify` skips once; `lidza verify --install-hook` sets it up again). |
| `lidza doctor` | toolchain, services, `node_modules`, pack builds, e2e browser; each missing item with its fix. |
| `lidza pack scaffold <name>` | creates `packs/<name>` with a crate and an example capability; `lidza pack build` compiles it. |
| `lidza db migrate\|rollback\|status` | applies `db/migrations` (db pack, `DATABASE_URL` from `.env`). |
| `lidza benchmark [--vus 500] [--duration 1m]` | runs `benchmarks/scale_test.js` with k6 against the running app; heap and goroutines must stay flat. |
| `lidza version` | prints the framework version. |
| `go test ./...` | Go tests. |
| `npm run check` | frontend type check and lint (`tsc --noEmit && eslint src`; `jsx-a11y` violations are errors). |


## Agent interface

`.mcp.json` (Claude Code) and `.gemini/settings.json` (Gemini CLI) start
`lidza mcp` for this project. Its tools:

| Tool | Returns |
|---|---|
| `lidza_routes` | the API routes with handler name, file, line and signature |
| `lidza_context` | the whole project as JSON (same as `.lidza/context.json`) |
| `lidza_check` | the diagnostics of `lidza check --json` |
| `lidza_logs` | the last lines of the `lidza dev` output (`lines`, `filter`) |
| `lidza_config` | `lidza.json` |
| `lidza_api` | the framework's public Go API (`package`, `filter`), also the resource `lidza://api` |
| `lidza_snippet` | a file of the reference app, verified by its tests (`name`: `schema`, `routes`, `auth-handlers`, `resource-handlers`, `queries`, `handler-test`, `mcp-tool`, `page`, `browser-test`); also `lidza snippet` |
| `lidza_errors` | captured errors (analytics pack) |

Its prompts are the recipes of this guide (`add-api-route`,
`add-resource`, ...). The same recipes are skills for Claude Code
(`.claude/skills/`) and Codex (`.agents/skills/`, invoked as
`$add-api-route`) and commands for Gemini CLI (`.gemini/commands/lidza/`,
invoked as `/lidza:add-api-route`); `lidza gen` rewrites all of them from
this file.

The app adds its own tools in `tools.go` with
`lidza.ToolFunc("name", "what it does", func(ctx, In) (Out, error))`;
they appear as `app_name` in `lidza mcp` (running inside the app, with
its packs) and, when the binary runs with `LIDZA_MCP_TOKEN`, at `/mcp`
for other agents. Give a tool a `schema.lidza` type as `In` and its rules
are enforced.

While `lidza dev` runs, http://127.0.0.1:3000/llms.txt summarizes the app
and http://127.0.0.1:3000/llms-full.txt has this guide plus every handler
signature. Both are regenerated after each Go rebuild.

## Rules

1. Go owns `/api`. The frontend never defines an API route and never proxies
   one; it calls `/api/...` on the same origin.
2. Add an API route in `routes.go` with `router.Route(r, "GET /api/v1/things/{id}", handler)`
   where `handler` is `func(ctx context.Context, req *router.Request[In]) (Out, error)`.
   `In` is the JSON body type (`router.None` without one) and `Out` the reply
   (`router.None` for 204). Put the shapes in `schema.lidza` so they get
   validation and reach the client. Return `router.NotFound("thing")` or
   `router.Errorf(status, ...)` for client-visible errors; any other error is a
   500 whose text stays on the server. Keep handlers under `/api/v1/`.
3. Name handlers: the name becomes the client method (`listPosts` gives
   `api.listPosts()`).
4. The frontend calls the API only through `@lidza/client` (`api.<name>`),
   never `fetch` by hand. The client is regenerated from the handlers; after
   changing a handler's types, `lidza check` shows what the frontend must adapt.
5. Never edit `dist/`, `schema/` or `.lidza/`; they are generated.
6. Do not run the frontend dev server or the Go binary by hand; `lidza dev`
   starts both and keeps them in sync.
7. Keep the app stateless: no global mutable maps, no in-memory sessions.
   State goes to Postgres or Redis.
8. Log with `lidza.Log(ctx).Info("what happened", "key", value)`: the line
   carries the request id, so one request's lines are found together.
   Never `fmt.Println` in handlers.
9. Before calling a framework function, read its signature: `lidza api
   [package] [--filter name]` or the MCP tool `lidza_api`; `lidza api app`
   (or `./handlers`) does the same for this app's own packages. An import
   of a framework package that does not exist fails `lidza check` (L004).
10. A pattern this app uses twice is a recipe: write it under "App
   recipes" (`lidza recipe add "Title"`) so the next task follows it.
11. Before writing a handler, page, test or tool of a kind you have not
   written here, read the matching snippet (`lidza snippet` or the MCP
   tool `lidza_snippet`): it is the reference app's code, verified by its
   tests.

## Templates

What each frontend template ships; the API contract, `lidza check`,
`lidza test` and the packs are the same for all.

| | react | svelte | astro | htmx |
|---|---|---|---|---|
| Rendering | client routes, prerendered at build; per-request SSR with `LIDZA_SSR=1` | one page, prerendered at build, hydrated | static pages at build | Go templates per request |
| Data | `@lidza/client` with TanStack Query, live refetch via `useLive` | `@lidza/client` in components | `@lidza/client` in page scripts | Go handlers, htmx partials |
| Accessibility | `jsx-a11y`, errors | Svelte compiler a11y checks, errors | `jsx-a11y` through `eslint-plugin-astro`, errors | none automated |
| Browser tests | `e2e/` (Playwright), `lidza test --e2e` | same | same | `pages_test.go` in Go |
| Time zone cookie | `src/timezone.ts` | same | same | inline in `views/layout.html` |
| Analytics reporter | `VITE_ANALYTICS=1` | `VITE_ANALYTICS=1` | `PUBLIC_ANALYTICS=1` | `ANALYTICS_FRONTEND=1`, `static/analytics.js` |
| Styling | Tailwind v4 | `app.css` | `<style is:global>` in the layout | `static/app.css` |

This app uses the **react** template.

## Tests

`routes_test.go` shows the shape: `srv := lidzatest.Start(t, app())`
boots the app with its packs (`.env.test`, `LIDZA_MODE=test`) and
`srv.JSON(t, method, path, body, &out, lidzatest.Bearer(token))` calls it.
Run `lidza test`.

Time and outbound HTTP are testable when handlers use `lidza.Now(ctx)`
instead of `time.Now()` and `lidza.HTTPClient(ctx)` instead of
`http.DefaultClient`: `srv.Clock.Set(t)` freezes time, and
`lidzatest.Start(t, app(), lidzatest.WithRecorder("name"))` replays
`testdata/http/name.json`, recorded once with `LIDZA_RECORD=1 lidza test`.

Browser tests live in `e2e/*.spec.ts` (Playwright); `lidza test --e2e`
runs them against the built binary. If the browser is missing the command
prints the install line; `lidza test --e2e --install` runs it.

## Recipes

Step-by-step tasks. Each one is also a prompt in `lidza mcp`, a skill in
`.claude/skills/<name>` and `.agents/skills/<name>`, and a Gemini command
in `.gemini/commands/lidza/<name>.toml`; `lidza gen` rewrites them from
the guide. This section is the framework's: `lidza gen` refreshes it when
the framework changes. This app's own recipes go under "App recipes"
below, which the framework never touches. Every recipe ends the same way: `lidza check
--json` (MCP: `lidza_check`) until `"status": "ok"`, then `lidza test`
(`lidza_test`).

### Start with the brief

Fill the app's brief with the developer before building: what it is
for, who owns the data, the design, the services, where it runs and how
you work together. The answers are the app's shared memory, in git.

1. `lidza_brief` (MCP) lists the open questions: each with why it is
   asked, its kind (`one`, `many`, `text`) and suggested answers.
   `lidza://brief` reads the file.
2. Ask the developer a few questions at a time with your question tool
   (in Claude Code, AskUserQuestion): the question, two to four
   suggestions (the tool's, adjusted to what you know of the app, the
   likeliest first) and room for their own answer; a `many` question
   takes several. Offer "later" and "skip" with every question, and
   "skip the rest" for the whole brief. Never answer for them. Later
   leaves the question open; skip (`lidza_brief_skip`, with the id, or
   no ids for every open question) closes it: it is not asked again.
3. Record each answer with `lidza_brief_answer`, in their words or the
   suggestion they picked. It lands in `docs/brief.md` and where it
   acts: a decision in `docs/decisions.md`, the working agreements in
   the agent files, the palette in the design tokens and
   `admin/theme.css`, a seeded app recipe ("Scope a query in this app",
   "Style a page to match the app", "Import or seed data"). Apply what
   the result lists under `manual` yourself.
4. The required questions first (purpose, users, journeys, ownership,
   sign-in, palette, pushing): `lidza check` warns while they are open
   (L015).
5. Summarize the answers and what was written, then commit `docs/`, the
   agent files, `src/index.css` and `admin/`.
6. Later, a fact the developer states goes in with `lidza_note_add`, and
   a changed answer with `lidza_brief_answer` again. In a terminal the
   same interview is `lidza brief`.

### Add an API route

Expose one operation under `/api/v1/` with typed input and output, so it
is validated on the server and callable from the client by name.

1. Declare the shapes in `schema.lidza`:

   ```
   type CreateThing {
     title string @min(1) @max(200)
   }
   type Thing {
     id    uuid
     title string
   }
   ```

2. Register the handler in `routes.go`:

   ```go
   router.Route(r, "POST /api/v1/things", createThing)

   func createThing(ctx context.Context, req *router.Request[schema.CreateThing]) (schema.Thing, error) {
   	req.Status(http.StatusCreated)
   	return schema.Thing{ID: "1", Title: req.Body.Title}, nil
   }
   ```

   `In` is `router.None` without a body, `Out` is `router.None` for 204.
   The body is decoded and validated (422 with field errors) before the
   handler runs. Return `router.NotFound("thing")` or
   `router.Errorf(status, ...)` for client-visible errors; any other error
   is a 500 whose text stays on the server. The handler name becomes the
   client method (`createThing` gives `api.createThing`).

   A reply that arrives in pieces (a model's text, progress) is a
   stream: `router.Stream(r, "POST /api/v1/things/draft", draftThing)`
   with `func draftThing(ctx context.Context, req
   *router.Request[schema.CreateThing], send func(string) error) error`.
   Each `send` reaches the client at once as a server-sent event; the
   event is a schema type, a string or a number. Return nil to end the
   stream. An error before the first `send` is an ordinary reply, after
   it the client gets it as an error with its status. `send` fails when
   the client has gone; return then. A stream's deadline is
   `App.StreamTimeout` (10 minutes by default; the generated clients ask
   with `Accept: text/event-stream`), not the 30 seconds of other
   requests.

3. Save. `lidza dev` regenerates `schema/`, rebuilds, and rewrites the
   client (without it: `lidza gen`, MCP `lidza_gen`). Check with
   `curl -s -X POST http://127.0.0.1:3000/api/v1/things -d '{"title":"x"}'`.

4. Call it from the frontend as `api.createThing({ title })` from
   `@lidza/client`; never `fetch` by hand (`lidza check` warns, L003).
   A stream's method returns an async iterable: `for await (const piece
   of api.draftThing({ title }, { signal })) text += piece`; leaving the
   loop or aborting the signal closes the request. The Dart client
   returns a `Stream`.

5. Add a test in `routes_test.go` (see "Write a test") and run
   `lidza check`, then `lidza test`.

### Add a resource

Give a model the five standard routes (list, get, create, patch, delete)
backed by Postgres. Needs the `db` pack (`lidza pack add db`).

1. Declare a `model` in `schema.lidza`:

   ```
   model Post {
     id        uuid     @id @default(uuid())
     title     string   @min(1) @max(200)
     body      string?
     createdAt time     @default(now())
   }
   ```

2. Run `lidza gen resource Post` (MCP: `lidza_gen_resource` with
   `model: "Post"`): it writes `db/queries/post.sql`, the
   `CreatePost`, `UpdatePost` and `PostList` types in `schema.lidza`,
   `handlers/post.go` with the routes under `/api/v1/posts`, and the
   registration line in `routes.go`.
3. Run `lidza db migrate` (MCP: `lidza_db_migrate`) to apply the new
   migration in `db/migrations/`.
4. The generated handlers file is ordinary code: mount the routes on a
   group (`handlers.PostRoutes(r.Group("/api/v1/posts", auth.Require()))`),
   add filters or ownership checks there (the snippet
   `resource-handlers` scopes every query to the signed-in user).
   Regenerate with `--force` to reset it.
5. `lidza check`, then `lidza test`.

### Add sign-in

Give the app accounts through the auth pack's own sign-in: email and
password by default, sign-in providers (Google, GitHub, Microsoft, any
OIDC issuer) when configured. The app writes no login handler and no
OAuth flow.

1. `lidza pack add auth` (MCP: `lidza_pack_add`; needs `db`, and `mail`
   for the verification and reset links). In `routes.go`:
   `auth.Mount(r, auth.Options{Title: "notes"})`. `lidza gen` then
   writes the client: `api.authRegister`, `authLogin`, `authLogout`,
   `authSession`, `authMe`, `authVerify`, `authForgot`, `authReset`,
   `authPassword`, `authDelete`, `authProviders`. Accounts live in
   `auth_user`; the app's rows carry the user's id,
   `auth.CurrentUser(ctx).ID` (recipe "Scope a query to the signed-in
   user").
2. Pages: the sign-in page calls `api.authLogin({ email, password })`
   and shows a 401 as "wrong email or password" and `err.fields` from a
   422; registration calls `api.authRegister`; the app shell reads
   `api.authSession()` once (`user` is null for a visitor). Render one
   button per entry of `api.authProviders()` as a plain link to its
   `url` (add `?redirect=/path` to land elsewhere than `/`); the callback
   sets the cookies and redirects, or lands on `/login?error=...`. The
   pages `/verify` and `/reset` read `?token=` and call `authVerify` or
   `authReset`.
3. Providers: `AUTH_PROVIDERS=google,github` in `.env` or on the admin
   pages (section "Sign-in providers"); the client id and secret in the
   credentials: `lidza credentials set AUTH_GOOGLE_CLIENT_ID=...
   AUTH_GOOGLE_CLIENT_SECRET=...`. Register the callback
   `<APP_URL>/api/v1/auth/google/callback` with the provider. Microsoft
   takes `AUTH_MICROSOFT_TENANT`; another OIDC issuer takes
   `AUTH_<NAME>_ISSUER` (and `AUTH_<NAME>_LABEL`). An identity whose
   email the provider vouches for joins the local account of that
   address; the admin Users page shows how each account signs in.
   Options: `NoRegister` (invite-only; `auth.From(ctx).CreateUser` adds
   accounts), `NoLocal` (providers only), `RequireVerified`, `Claims`
   (roles into the token), `AfterSignIn`.
4. New accounts: `OnSignUp` runs once per account the routes create (a
   registration, or a provider's first sign-in that makes a new
   account; not a later sign-in, not an identity joining an existing
   account, not `CreateUser`), inside the transaction that inserts it.
   Write the app's rows there, with the transaction:

   ```go
   auth.Mount(r, auth.Options{
   	OnSignUp: func(ctx context.Context, tx pgx.Tx, s auth.SignUp) error {
   		// s.Method is "password" or the provider ("google"); s.Lang the request's language.
   		return queries.New(tx).CreateProfile(ctx, queries.CreateProfileParams{UserID: s.Profile.Subject, Lang: s.Lang})
   	},
   	OnDeleteUser: func(ctx context.Context, tx pgx.Tx, subject string) error {
   		return queries.New(tx).DeleteProfile(ctx, subject)
   	},
   })
   ```

   An error from `OnSignUp` rolls the account back: registration replies
   with it (`router.Errorf(403, ...)` as is), a provider sign-in lands on
   `/login?error=signup`. Do not detect sign-ups in `Claims`.
   Every sign-in (a password, a provider, and the one after a sign-up)
   runs `OnSignIn(ctx, auth.SignIn{Profile, Method})` before the session
   opens: claim invitations sent to the user's email there, or finish a
   join they started signed out. An error refuses the sign-in
   (`?error=signin` for a provider). Token refreshes do not run it, and
   neither does `Claims`, which runs for every token and is for claims
   only.
5. Deleting an account: set `OnDeleteUser` (it deletes or anonymizes
   the app's rows of the subject; a no-op when the app keeps none), which
   also serves the route. The account page links to a form that calls
   `api.authDelete({ password })`; an account without a password (a
   provider only) sends `{}` and must have signed in within ten minutes,
   else a 403 with code `reauthenticate` (send the user through the
   provider's `url` with `?redirect=` back to the form); a wrong password
   is a 403 with code `wrong_password`. Switch on `err.code` of the
   `ApiError`, not the message. The
   route removes the user, identities, sessions and tokens, runs
   `OnDeleteUser` in the same transaction for the app's rows (an error
   rolls it all back), and clears the cookies. From the app or an admin
   action: `auth.From(ctx).DeleteUser(ctx, id)`; an app with its own
   users table uses `DeleteUserTx(ctx, tx, id)` in its transaction.
6. Emails: with the mail pack the links go out as plain text, or
   through `mail/auth_verify.txt.tmpl` and `mail/auth_reset.txt.tmpl`
   when the app has them (Data: `App`, `Link`, `Email`, `Name`). A
   language's copy is `mail/auth_verify.<lang>.txt.tmpl` (`de`, `pt-BR`),
   chosen by the request's language (recipe "Send an email"); it names
   its subject with `{{define "subject"}}...{{end}}`.
7. Test: `lidzatest.Start`, `POST /api/v1/auth/register`, then a scoped
   route with the cookies the reply set. The providers are tested by the
   framework (against an OIDC issuer in a test server), not by the app.
   `lidza check`, then `lidza test`.

### Scope a query to the signed-in user

Make a resource answer only with the rows its user may see: the
generated handlers are unscoped, and a row another user may not read is
a 404, never a 403 (the reply must not confirm it exists).

1. Mount the routes behind `auth.Require()` and take the user from the
   context: `user := auth.CurrentUser(ctx).ID` (never from the body; drop
   `ownerId` from the `Create` and `Update` types the generator wrote).
2. Owned rows: add `AND owner_id = $N` to every statement in
   `db/queries/<table>.sql` (list, count, get, update, delete) and pass
   the user; set `owner_id` from the user on create. The snippets
   `queries` and `resource-handlers` are this case.
3. Shared rows (a team, a project): keep a membership table
   (`Membership { projectId @ref(Project, cascade), userId @ref(User,
   cascade), role }` with `@@unique(projectId, userId)`) and join on it:

   ```sql
   -- name: GetTask :one
   SELECT t.* FROM task t JOIN membership m ON m.project_id = t.project_id
   WHERE t.id = sqlc.arg('id') AND m.user_id = sqlc.arg('user_id');
   ```

   Lists and creates that take the parent id from the path check it
   first with one helper (`requireMember(ctx, projectID)` in
   `handlers/access.go`: a membership lookup, 404 when absent, 403 for a
   role that may read but not do this). Qualify columns (`t.id`) in an
   `UPDATE ... WHERE id IN (SELECT ...)`, or sqlc reports them ambiguous.
4. Reply `router.NotFound("task")` on `pgx.ErrNoRows` and on zero rows
   affected.
5. Test it: a second user lists nothing and gets 404 on the first user's
   id; a member of the project sees the row. `lidza check`, then
   `lidza test`.

### Add a page

Add a client-side route rendered by React, prerendered at build time.

1. Create `src/pages/Things.tsx` exporting a component.
2. In `src/router.tsx`, declare `createRoute({ getParentRoute: () => rootRoute, path: '/things', component: Things })` and add it to the route tree.
3. Fetch data with `useQuery({ queryKey: ['things'], queryFn: () => api.listThings() })`
   from `@lidza/client`; never `fetch` by hand.
4. Routes without parameters are prerendered by `npm run build`; keep
   the first render free of browser-only APIs (`window`, `localStorage`),
   read them in effects. A route with a `loader` that calls `api.*` is
   not prerendered (no API at build time; the build says so) and renders
   in the browser, unless the binary runs with `LIDZA_SSR=1`: then every
   page renders per request in a Node sidecar, the loader runs on the
   server with the visitor's cookies forwarded, and the page arrives with
   its data and the router's hydration payload, so the loader does not
   run again in the browser.
5. Live data: `useLive(['things'])` (src/live.ts) refetches the `things`
   queries when a handler publishes to that topic on the `realtime` pack.
6. Forms: `validators.CreateThing(values)` from `@lidza/client` returns the
   field errors the server would, before the request.
7. Every element must be accessible: `lidza check` fails on `jsx-a11y`
   errors (missing `alt`, click handlers on non-interactive elements, ...).
8. Style with Tailwind utilities; colors and fonts come from the `@theme`
   tokens in `src/index.css` (`bg-brand`, `text-ink`, `border-line`,
   `text-muted`, `text-danger`). Change the tokens, not the classes, to
   rebrand. No other CSS framework.
9. Text in more than one language (the `i18n` pack, one
   `locales/<lang>.json` per language): write every string as
   `t('things.title')` from `src/i18n.ts`, never a literal, and add the
   key to every catalog (`t('things.count', n)` fills a `%d` or `%s`).
   `npm run build` renders each page once per locale and the binary
   serves the visitor's, with `<html lang>` and its catalog, so the first
   paint and hydration are in that language. Format numbers and dates
   with `Intl` and `locale()`; a language switch calls `setLocale('sq')`.
10. Add a browser test in `e2e/things.spec.ts` (see "Write a test"), then
   `lidza check` and `lidza test --e2e`.

### Add a pack capability

Run work in Rust, compiled to WASM and called from a handler with a
deadline: for code that must be contained, a crate Go lacks, or heap
pressure. Read "Rust: when and how" first; it is not for speed.

1. Record why this module is Rust (`lidza decision add "<module> in a
   Rust pack" --why "contained input | crate X | heap pressure"`, MCP
   `lidza_decision_add`), then `lidza pack scaffold <name>`: it creates
   `packs/<name>` with a crate and an example capability (skip when the
   pack exists).
2. Declare the input and output types in `schema.lidza`.
3. In `packs/<name>/rust/src/lib.rs` write
   `lidza_export!(capability, |input: In| -> Result<Out, String>)`; the
   Rust structs come from `packs/<name>/rust/src/schema.rs`, generated.
4. List the capability in `packs/<name>/pack.lidza.json` with its input
   and output type names.
5. `lidza check` builds the module and writes `packs/<name>/pack.go`.
   From a handler: `out, err := <name>.From(ctx).<Capability>(ctx, in)`.
   The MCP tool `pack_<name>_<capability>` runs it directly.

### Add an MCP tool

Let an agent call a function of this app, with its packs, from
`lidza mcp` (as `app_<name>`) and from the running binary at `/mcp`.

1. In `tools.go` add to the list returned by `tools()`:

   ```go
   lidza.ToolFunc("count_posts", "Number of posts.", func(ctx context.Context, _ struct{}) (int, error) {
   	var n int
   	err := db.From(ctx).QueryRow(ctx, "SELECT count(*) FROM post").Scan(&n)
   	return n, err
   }),
   ```

2. Give the input a `schema.lidza` type to have its rules enforced; the
   input schema shown to the agent comes from the Go type.
3. `lidza check`; then restart `lidza mcp` (the MCP client reconnects) and
   call `app_count_posts`.

### Send an email

Send a transactional email (verification, reset, receipt) through the
mail pack, never through a vendor SDK.

1. `lidza pack add mail` (MCP: `lidza_pack_add`; after `db`, and `jobs`
   for background delivery);
   set `MAIL_FROM` and, in production, `MAIL_PROVIDER` with its key in
   `.env`. `.env.test` gets `MAIL_PROVIDER=outbox`.
2. Write the bodies as Go templates: `mail/<name>.txt.tmpl` (always) and
   `mail/<name>.html.tmpl` (optional), over the `Data` you pass. Other
   languages: `mail/<name>.<lang>.txt.tmpl` (`verify.de.txt.tmpl`,
   `verify.pt-BR.txt.tmpl`). Send picks `<name>.<lang>`, then the base
   language (`pt` for `pt-BR`), then `<name>`, for the message's `Lang`,
   else the request's language: the `i18n` pack's locale when it runs,
   else `Accept-Language`. Outside a request (a job), set `Lang` from
   the user's stored language. A template that defines
   `{{define "subject"}}...{{end}}` in its
   `.txt.tmpl` sets the subject, so it is translated with the body.
3. From a handler or a job:

   ```go
   _, err := mail.From(ctx).Send(ctx, mail.Message{
   	To: user.Email, Subject: "Verify your email", Template: "verify",
   	Data: map[string]string{"Link": mail.From(ctx).Link("/verify?token=" + token)},
   })
   ```

   `Link` makes the path absolute with `APP_URL` (`.env`: the address
   the app is reached at from an inbox), so the link works outside the
   outbox. Send returns once the row is in the outbox (queued for the
   jobs pack, or delivered right away without it). Do not build the
   message with `fmt.Sprintf` and do not call the vendor's API.
4. Test it: `mail.From(srv.Context()).WaitFor(ctx, to, "Verify", 5*time.Second)`
   returns the newest message to that address whose subject contains the
   text, waiting for one a job sends; `Outbox(ctx, 5)` lists them newest
   first. Both have `Status`, `Text` and `HTML`; read the link out of the
   text. The snippet `auth-handlers` and `routes_test.go` in the
   reference app show both sides.
5. `lidza check`, then `lidza test`. In dev, `lidza_mail` (MCP) shows the
   outbox.

### Add a background job

Do work outside the request (send a notification, resize an upload, call
a slow API) through the jobs pack: the request enqueues, a worker runs
the handler later with retries, on this node or another. Recurring work
(every 15 minutes, Mondays at 09:00) is a schedule the pack enqueues.

1. `lidza pack add jobs` (MCP: `lidza_pack_add`; after `db`).
2. Declare the payload in `schema.lidza` (`type NotifyTask { taskId uuid
   actorId uuid }`) so both sides share it.
3. Write the handler, `func NotifyTask(ctx context.Context, payload
   json.RawMessage) error` in `handlers/notify.go`: decode the payload,
   read the current state by id (the job runs after the request, so the
   row may have changed or gone), do the work. The context carries the
   packs: `db.From(ctx)`, `mail.From(ctx)`, `realtime.From(ctx)` work as
   in a request handler. Return an error to retry (backoff, up to
   `JOBS_MAX_ATTEMPTS`). A job can run more than once: after a retry, or
   after a shutdown, which gives a running job `JOBS_DRAIN` (1s) to
   finish, then cancels its context and puts it back to pending for the
   next node or the restart. Stop when `ctx` is done and make a rerun
   harmless (upserts, a done marker per item).
4. Register it in `start.go`:

   ```go
   jobs.FromServices(s).Handle("notify.task", handlers.NotifyTask)
   ```

5. Enqueue from the handler that caused it: `jobs.From(ctx).Enqueue(ctx,
   "notify.task", schema.NotifyTask{...})`, or inside a transaction
   `EnqueueTx(ctx, tx, ...)` so the job is committed with the write and
   never runs for one that rolled back. `jobs.RunAt(t)` delays one run.
   Never do the work in the request as a fallback.
6. Work on a timetable (a weekly digest, a sync every 15 minutes) is a
   schedule in `start.go`, not a job that enqueues its next run:

   ```go
   q := jobs.FromServices(s)
   q.Handle("digest.weekly", handlers.WeeklyDigest)
   if err := q.Schedule("digest.weekly", jobs.Weekly(time.Monday, "09:00", "Europe/Tirane"), nil); err != nil {
   	return err
   }
   ```

   `jobs.Every(15*time.Minute)` runs at :00, :15, :30 and :45;
   `jobs.Daily("06:30", zone)` and `jobs.Weekly(day, "09:00", zone)`
   run at a clock time in a time zone ("" is UTC), once on the days the
   clocks change. The payload (here `nil`) goes with every run. However
   many nodes run, one job is enqueued per due time; after downtime one
   missed run is enqueued, not one per missed time. The `job_schedule`
   table comes from `schema.lidza`: `lidza gen`, then `lidza db
   migrate`. The admin Jobs page lists each schedule with its next and
   last run.
7. Test it: the job runs a moment after the reply. For mail,
   `mail.From(srv.Context()).WaitFor(...)`; otherwise poll
   `jobs.From(srv.Context()).Get(ctx, id)` until `State` is `done`. A
   scheduled job's handler is tested by enqueuing its kind directly.
8. `lidza check`, then `lidza test`.

### Publish live updates

Let every open page of a resource see a change without reloading,
through the realtime pack: handlers publish on a topic after a write,
the page subscribes and refetches.

1. `lidza pack add realtime` (MCP: `lidza_pack_add`). More than one
   node needs `REALTIME_BUS_URL` (Valkey) so a publish on one reaches
   the sockets on the others.
2. Name topics by resource, `project:<id>`, in one function both sides
   use.
3. Mount the socket behind auth and authorize each topic; without
   `Authorize`, any signed-in user can subscribe to any topic:

   ```go
   live := realtime.Handler(realtime.Authorize(func(r *http.Request, topic string) bool {
   	id, ok := strings.CutPrefix(topic, "project:")
   	return ok && isMember(r.Context(), id, auth.CurrentUser(r.Context()).ID)
   }))
   r.Group("/api/v1/realtime", auth.Require()).Handle("GET /api/v1/realtime", live)
   ```

4. Publish after the write is committed, ids and an event name only:
   `realtime.From(ctx).Publish(ctx, topic, schema.Change{Kind: "task.moved",
   ID: task.ID})`. The page refetches through the API, which enforces
   access; the row itself never travels over the socket.
5. In the page, key the queries by the topic and call `useLive([topic])`
   (`src/live.ts`): every message invalidates the queries whose key
   starts with it.
6. Test it in Go: `websocket.Dial` (`github.com/coder/websocket`, already
   a dependency) with `lidzatest.Bearer`'s header on
   `ws://.../api/v1/realtime?topics=...`, make the change through the
   API, read one message. A signed-out dial must be refused.
7. `lidza check`, then `lidza test`.

### Add an LLM feature

Put a language model behind an API route: a chat reply, a summary, a
structured extraction into a schema type, or a model that uses the
app's tools. The llm pack speaks the providers; the app never does.

1. `lidza pack add llm` (MCP: `lidza_pack_add`). `.env` gets
   `LLM_PROVIDER=fake`; set `ollama` for a local model, or `anthropic`,
   `openai` or `google` with `LLM_API_KEY` and, when the default does not
   suit, `LLM_MODEL`. `.env.test` keeps `fake`. Try the setup with the
   MCP tool `lidza_llm` before writing code. Embeddings come from the
   chat provider unless `EMBED_PROVIDER` names another: `openai`,
   `google`, `ollama` or `compatible`, with `EMBED_API_KEY`,
   `EMBED_MODEL` and `EMBED_BASE_URL` as needed. Anthropic has no
   embedding model, so an Anthropic app that embeds sets it;
   `EMBED_PROVIDER=none` turns embeddings off. Keys go in the
   credentials (`lidza credentials set EMBED_API_KEY=...`).
2. Declare the output in `schema.lidza` when the reply is data, not
   prose:

   ```
   type NoteTags {
     tags string[] @min(1) @max(5)
   }
   ```

3. In the handler, read the rows the user may see (the route runs behind
   `auth.Require()` and the usual scoping), then call the model:

   ```go
   out, err := llm.Generate[schema.NoteTags](ctx, llm.From(ctx), llm.Request{
   	System:   "Tag notes with one to five short lowercase topics.",
   	Messages: []llm.Message{ {Role: llm.User, Content: note.Title + "\n\n" + note.Body} },
   	Label:    "note.tags",
   })
   ```

   `Label` names the feature: with the db pack every call is a row in
   `llm_usage` under it, so the usage report and the admin's Language
   model page show tokens per feature.

   `Chat` returns prose, `Stream` delivers it as it arrives (pass each
   piece to the `send` of a `router.Stream` route, see "Add an API
   route", or publish it on the realtime pack), and
   `Run(ctx, req, tools())` lets the model call the app's tools with the
   packs in its context. Keep the prompt in the handler or a `prompts/`
   file, never in the frontend, and never send a row the user may not
   read.

   Embeddings for search or similar items:

   ```go
   res, err := llm.From(ctx).Embeddings(ctx, llm.EmbedRequest{
   	Texts: []string{post.Title + "\n\n" + post.Body},
   	Label: "post.index",
   })
   ```

   `res.Vectors` has one vector per text, `res.Usage.Input` the tokens
   (Gemini reports none); the call is a row in `llm_usage` under its
   label, as a chat is. `Embed(ctx, texts)` is the same call without a
   label. When nothing can embed (`EMBED_PROVIDER=none`, or Anthropic
   without `EMBED_PROVIDER`) the error wraps `llm.ErrNoEmbeddings`;
   test it with `errors.Is` to fall back to keyword search.
4. Long calls (a summary of many rows, a batch) go through the jobs
   pack; the request enqueues and the page reads the result later.
5. Test with the fake: `.env.test` has `LLM_PROVIDER=fake`, so
   `llm.From(srv.Context()).Fake().ReplyJSON(schema.NoteTags{Tags:
   []string{"go"}})` scripts the next reply and `Fake().Calls()` shows
   the prompt the handler sent. Assert on both. The snippet
   `llm-handler` and `routes_test.go` in the reference app show it. The
   fake embeds too, the same vector for the same text. `.env.test` has
   `LLM_PROVIDER=fake` and `EMBED_PROVIDER=fake` (setup writes them,
   `lidza update` adds them), so a provider set in `.env` never reaches
   the tests.
6. `lidza check`, then `lidza test`.

### Store a file

Keep uploads and generated files in object storage through the storage
pack, with the app deciding who may read them.

1. `lidza pack add storage` (MCP: `lidza_pack_add`). Development and
   tests use `STORAGE_PROVIDER=local` (files under `storage/`);
   production sets `s3` with `STORAGE_BUCKET`, `STORAGE_ENDPOINT` and
   the keys in the credentials (`lidza credentials set
   STORAGE_ACCESS_KEY=... STORAGE_SECRET_KEY=...`).
2. Accept the upload in a raw handler (a body is not JSON, so not
   `router.Route`): `r.HandleFunc("PUT /api/v1/notes/{id}/attachment",
   ...)` behind the same access check as the note, read the body up to
   a limit (`http.MaxBytesReader`), and store it under a key that names
   the owner and the row: `notes/<id>/attachment`. Keep the key in a
   column of the row. The generated client calls it with the file as
   the raw body: `api.uploadAttachment({ id }, { body: file })` (the
   File's type becomes the Content-Type; `contentType` overrides it);
   no hand-written `fetch`. A list that takes filters reads them with
   `req.Query("q")` and the client sends them as `{ query: { q, limit } }`.
3. Read it back through the app (`storage.Handler` or a handler that
   checks access and streams `Get`), or hand the browser a
   `PresignGet` URL for a minute; never a permanent public URL for a
   private file. Delete the object when the row goes.
4. Test with the local provider: `.env.test` sets `STORAGE_PROVIDER=local`
   and a `STORAGE_DIR`; upload through the API, read it back, delete the
   row and check `Stat` returns `storage.ErrNotFound`. The snippet
   `storage-handler` and `routes_test.go` in the reference app show it.
5. `lidza check`, then `lidza test`.

### Add the admin pages

Give operators a place to watch the packs and set their providers,
without writing a page.

1. `lidza pack add auth` if the app has no accounts yet; the pages are
   behind `auth.Require()`.
2. In `routes.go`: `admin.Mount(r, admin.Options{Title: "notes"})`
   (import `github.com/agim/lidza/packs/admin`).
3. Sign in first: the first account is an admin. More are added on the
   Overview page, or from the project with `lidza admin add
   you@example.com` (`ADMIN_USERS` in the credentials, read within
   seconds). `.env.test` names a test account. A script or browser test
   run against `lidza dev` signs up in the dev database: its account may
   be the first, so add the developer with `lidza admin add`.
4. Each pack's page has a Settings tab (Mail, Language model, Storage;
   sign-in providers under Users): pick the provider and only its
   fields show. Values are saved sealed with the master key and applied
   without a restart; one set in the process environment wins and the
   field says so.
5. Theme it when the app has a look: `admin/theme.css` sets
   `--admin-accent`, `--admin-accent-fg`, `--admin-bg`, `--admin-surface`,
   `--admin-line`, `--admin-fg`, `--admin-muted`, `--admin-sidebar`,
   `--admin-font` and `--admin-radius`, per theme under
   `[data-bs-theme=light]` and `[data-bs-theme=dark]`, or any Tabler
   variable. The default is Līdza's palette (a mulberry accent on warm
   neutrals); match the app's own tokens instead of a stock blue.
6. Test it: an admin gets 200 on `/admin/`, another user 403, a visitor
   401; the reference app's `routes_test.go` shows it.
7. `lidza check`, then `lidza test`.

### Extend the admin pages

Give the app's own operations a page in the admin pages (orders to
refund, a moderation queue, a report), or its own keys a settings form,
instead of a separate admin screen.

1. Settings: add an `admin.Section` to `admin.Options.Sections` for the
   keys the app reads with `pkg/env` (a payment provider's secret, a
   feature switch). Each `admin.Field` names its environment variable;
   `Kind` is `text`, `number`, `secret`, `select` or `multi`; a
   `Selector` field with `For` on the others shows only the fields of
   the chosen provider. They appear on the Settings page, saved like
   the packs'. A service that holds the value implements
   `Reconfigure(ctx) error` to pick up a saved change.
2. A page: add an `admin.Page` to `admin.Options.Pages` from a function
   in `handlers/admin.go` (snippet `admin-page`): `Name`, `Path`
   (`orders` serves `/admin/orders`), an `Icon` from the admin pack's
   `assets/icons.txt`, `Template`, `Data` (sqlc queries; the page is
   admin-only, so a query may cross accounts) and `Actions` (a form
   posts to `<Path>/<action>`; return the message the page shows, or an
   error).
3. The template is `admin/<page>.html` defining `content` (snippet
   `admin-page-template`): Tabler's classes (`card`, `table card-table`,
   `badge`, `btn`), the functions `icon`, `since`, `num`, `bytes`,
   `dict`, and `{{template "admin-empty" (dict "Icon" "inbox"
   "Title" "..." "Text" "...")}}` for an empty list. Embed it so it
   ships in the binary: `//go:embed admin/*.html` in `routes.go` and
   `Templates: lidza.Sub(adminFiles, "admin")`. No inline `<script>` or
   `style=`: the pages hold under a strict Content-Security-Policy; a
   destructive form takes `data-admin-confirm="..."`.
4. Record it in the same commit: `lidza decision add "Admin page
   Orders" --why "..."` (MCP `lidza_decision_add`), naming what the page
   lets an admin do and which rows it reaches across accounts.
5. Test it: the page answers 200 with a row for an admin and 403 for
   another user; the action changes the row (post the form with
   `Sec-Fetch-Site: same-origin`, as a browser does). `lidza check`,
   then `lidza test`.

### Add a recipe

Record a convention of this app so the next task follows it: a pattern
used twice (how lists paginate, how ownership is checked, how a webhook
is verified, how a report is built) is a recipe.

1. `lidza recipe add "Paginate a list"` appends a skeleton under "App
   recipes" in `docs/lidza-guide.md` (or write the `###` section by hand;
   from an agent, the MCP tool `lidza_recipe_add` takes the title,
   description and steps).
2. Fill it in: one sentence on when it applies, then numbered steps that
   name the file to open, the function or type to use, the command to
   run, and the check at the end. Point at a file in this app that does
   it already.
3. `lidza gen` (or the next check) turns it into the prompt, the skills
   and the command, and lists it in `CLAUDE.md`, `AGENTS.md` and
   `GEMINI.md`. Restart `lidza mcp` to see the new prompt.

### Write a test

Cover a handler with a Go test that boots the app, or a page with a
browser test.

1. Handler: in `routes_test.go` (or `handlers/<name>_test.go`):

   ```go
   func TestCreateThing(t *testing.T) {
   	srv := lidzatest.Start(t, app())
   	var out schema.Thing
   	res := srv.JSON(t, "POST", "/api/v1/things", schema.CreateThing{Title: "x"}, &out)
   	if res.StatusCode != http.StatusCreated || out.Title != "x" {
   		t.Fatalf("%d %+v", res.StatusCode, out)
   	}
   }
   ```

   `lidzatest.Start` boots the app with its packs (`.env.test`,
   `LIDZA_MODE=test`); `srv.JSON(t, method, path, body, &out,
   lidzatest.Bearer(token))` calls it. Freeze time with `srv.Clock.Set(t)`
   when the handler uses `lidza.Now(ctx)`; replay outbound HTTP with
   `lidzatest.WithRecorder("name")` when it uses `lidza.HTTPClient(ctx)`
   (recorded once with `LIDZA_RECORD=1 lidza test`).
2. Run `lidza test` (MCP: `lidza_test`): it creates and migrates the
   test database, runs `go test ./...`, then the frontend check.
3. Page: in `e2e/<name>.spec.ts` (Playwright) load the page, assert on
   text or roles, and assert no `window` errors (see `e2e/home.spec.ts`).
   Run `lidza test --e2e` (add `--install` once if the browser is
   missing).

## App recipes

This app's own conventions, one recipe each; the framework never edits
this section. Add one with `lidza recipe add "Title"` or by hand (see
"Add a recipe").

### Scope a query in this app

Every row belongs to one user and only they see it: the brief's ownership model (docs/brief.md).

1. Give the model an `ownerId uuid @index` and set it from `auth.CurrentUser(ctx).ID` on create, never from the body.
2. Add `AND owner_id = $N` to every query in `db/queries/<table>.sql`: list, count, get, update, delete.
3. Reply `router.NotFound` for another user's row, never 403.
4. Test it: another user lists nothing and gets 404 on the first user's id; `lidza check`, then `lidza test`.

### Style a page to match the app

Build a page that looks like the rest of this app: its palette, type, mood and themes, from the brief's Design section, never a stock look.

1. Read the Design section of `docs/brief.md` (palette, typography, mood, themes, languages); the tokens are in `src/index.css` (`bg-brand`, `text-ink`, `bg-surface`, `border-line`).
2. Use the tokens, never raw colours or a stock blue; status colours (green, yellow, red) only for status.
3. Every list gets a loading skeleton, an empty state with an action and a keyboard path; motion stays under 300 ms and respects `prefers-reduced-motion`.
4. Strings go through the i18n pack when the brief names more than one language.
5. Check both themes when the brief asks for light and dark, and a phone width.
6. `lidza check` (accessibility is enforced), then `lidza test --e2e`.

## Packs

A pack is Rust compiled to WASM, run by the app in a bounded pool with a
deadline per call. `pack.lidza.json` lists its capabilities; each names an
input and an output type from `schema.lidza`. `lidza gen` writes
`packs/<name>/pack.go`, so from a handler:

```go
out, err := geo.From(ctx).GeoDistance(ctx, req.Body)
```

To add one, follow the recipe "Add a pack capability". `lidza dev`
rebuilds the module when the crate changes.

## Rust: when and how

Go first. Handlers, queries, jobs, anything that talks to the database,
the network or the file system is Go, and stays Go. Rust is the compute
plane, reached only through a pack, and it is not there for speed: the
WASM sandbox costs more than it saves on this kind of work.

Measured (`go test ./pkg/engine -bench .` in the framework, one core of
an Intel Xeon E5-2407 v2, wazero 1.12, opt-level 3; treat as orders of
magnitude):

| | Go | Rust pack, `uninterruptible` | Rust pack, default |
|---|---|---|---|
| boundary: call with 100 B of JSON | | 12 µs | 44 µs |
| boundary: 1 MB of JSON | | 15 ms | 129 ms |
| brute-force nearest neighbours, 2000×500 points | 4.1 ms | 11.5 ms | 56 ms |
| word frequencies over 200 KB of text | 10.3 ms | 16.5 ms | 91 ms |

So reach for a pack when one of these is true, not otherwise:

1. **The code must be contained.** It transforms user-supplied input
   (images, documents, uploads, formulas) where a bug or a hostile input
   could loop, blow up memory or panic. A capability runs with a memory
   cap and a deadline; the worst case is one failed call, never a crashed
   process.
2. **A Rust crate does what you need** and Go has no equivalent worth
   the port: image codecs, geospatial indexes, parsers.
3. **The Go heap is the bottleneck**: a workload that allocates in the
   hundreds of megabytes and pins the garbage collector. The pack's
   memory is its own and freed as a block.

Not for: anything under a millisecond of work (the boundary costs more),
anything that needs I/O, or a hot path where the numbers above matter.
Measure with `lidza benchmark` before and after.

How, in short (the recipe "Add a pack capability" has the steps; the
snippets `pack-capability` and `pack-manifest` are working code):

- Types in `schema.lidza`; the capability in
  `packs/<name>/rust/src/lib.rs` as `lidza_export!`; the manifest lists
  it; the handler calls `<name>.From(ctx).<Capability>(ctx, in)`.
- Keep the input small and the output smaller: JSON crosses the boundary
  at roughly 70 MB/s. Send an id and let Go fetch, or send the bytes
  once, not per item.
- Set `"uninterruptible": true` in the manifest when every loop is
  bounded by the input (one pass over a text, over the pixels of an
  image, over a list): loops then run 3 to 8 times faster. Leave it off
  for code whose loops depend on the data's shape (parsers, solvers,
  anything recursive): the default inserts a deadline check in every
  loop, so a runaway call is cut off at `timeout_ms` instead of holding
  an instance until it returns.
- Errors: return `Err("message")` for input problems (the caller sees a
  400 with that text); panics are contained and reported as an error.
- Never do I/O in Rust: the sandbox has no network and no file system,
  by design.

Official Go packs, configured from `.env` (see `.env.example` after
`lidza pack add`):

- `db`: `db.From(ctx)` is a `*pgxpool.Pool`; SQL in `db/queries/*.sql`
  becomes typed Go via sqlc (`queries.New(db.From(ctx)).Name(ctx, ...)`).
- `auth`: `auth.HashPassword`/`CheckPassword`; `auth.From(ctx).Login(ctx,
  userID, claims)` returns tokens, `req.SetCookie` each of
  `auth.From(ctx).Cookies(tokens)` for browsers; protect a group with
  `g := r.Group("/api/v1/notes", auth.Require())` and read
  `auth.CurrentUser(ctx)`; `auth.Optional()` for a route that serves
  visitors too (the user is nil then). Logout:
  `auth.From(ctx).Logout(ctx, user.SessionID)`. Hardening: wrap the
  credential routes with `auth.Throttle()` (per-client rate limit,
  `AUTH_LOGIN_RPS`), refuse weak passwords with
  `auth.From(ctx).ValidatePassword(password, email)` (length, common
  passwords, the email itself), and run email verification and password
  reset on one-time tokens: `IssueToken(ctx, auth.PurposeVerifyEmail,
  email, 0)` makes the token the app mails, `ConsumeToken` redeems it
  once; `RevokeAll` after a reset. Working code: the snippets `routes`
  and `auth-handlers`.
- `jobs`: register handlers in `OnStart` with
  `jobs.FromServices(s).Handle("kind", fn)`; enqueue with
  `jobs.From(ctx).Enqueue(ctx, "kind", payload, jobs.RunAt(t))`.
- `cache`: `cache.Remember(ctx, cache.From(ctx), "key", ttl, load)`;
  `cache.From(ctx).Invalidate(ctx, "prefix:")` after writes.
- `i18n`: `i18n.From(ctx).T(ctx, "key", args...)`, `Number`, `Currency`,
  `Date`, `Time`, `DateTime` (in the visitor's zone: the template sets a
  `tz` cookie, API clients send `X-Timezone`; `I18N_TIMEZONE` is the
  default); catalogs in `locales/<lang>.json`; serve them with
  `r.Handle("GET /api/v1/i18n/{lang}", i18n.Handler())`. Store and send
  times in UTC; format at the edge.
- `realtime`: `realtime.From(ctx).Publish(ctx, topic, value)` and
  `r.Handle("GET /api/v1/realtime", realtime.Handler())`.
- `mail`: `mail.From(ctx).Send(ctx, mail.Message{To, Subject, Template:
  "verify", Data: data})` renders `mail/verify.txt.tmpl` and
  `mail/verify.html.tmpl` (Go templates over `Data`) and delivers through
  `MAIL_PROVIDER` (`mailgun`, `sendgrid`, `postmark`, `resend`, `smtp`;
  `log` by default, `outbox` in tests). With the `db` pack every message
  is a row in `mail_message` (`Outbox(ctx, n)`, the MCP tool
  `lidza_mail`); with the `jobs` pack delivery runs as a job with
  retries. Never import a vendor SDK (`lidza check` L006).
- `analytics` (opt-in): server errors are captured on their own; register
  `r.Handle("POST /api/v1/analytics/{kind}", analytics.Handler())`, set
  `VITE_ANALYTICS=1`, and call `analytics.From(ctx).Track(ctx, "name",
  props)` or `track()` from `src/analytics.ts`. The MCP tool
  `lidza_errors` shows what broke.

Order in `lidza.json` matters: `lidza/db` before `lidza/auth` and
`lidza/jobs`.

## Flutter or other Dart clients

Set `"sdk": {"dart": "clients/dart"}` in `lidza.json`; `lidza gen` writes
the `lidza_client` Dart package there with the same operations as
`@lidza/client`.

## Models and migrations

A `model` in `schema.lidza` is a table. `lidza gen` writes the full DDL to
`db/schema.sql` and, when models changed since `db/schema.lock.json`, a
numbered pair in `db/migrations/` (`NNNN_name.up.sql`, `.down.sql`).
Statements that lose data or can fail on existing rows carry a
`-- review` comment. Applying migrations is the `db` pack's job.
## Operations

The binary serves `/healthz` (liveness), `/readyz` (503 while a pack's
check fails: database ping, bus) and `/metrics` (Prometheus: requests by
route pattern, durations, pool and connection gauges). In dev,
`/debug/pprof/` too. `lidza check` has rules of its own: package-level
maps or slices (L001) and goroutines started in handlers (L002), because
state belongs in Postgres or Valkey and background work in a bounded
worker or the jobs pack; hand-written `fetch` of `/api` (L003); imports
of packages that do not exist or are not declared (L004); handler types
not declared in `schema.lidza` (L005).
Rate limit a route group with `r.Use(middleware.RateLimit(middleware.RateLimitOptions{RPS: 10, Burst: 20}))`;
guard an outbound dependency with `resilience.New(...)`.

## Deployment

`lidza build` makes `bin/notes`: the frontend embedded, no Node at
runtime (except `LIDZA_SSR=1`). `Dockerfile` builds the same into an
image that runs as a non-root user on port 3000; `deploy/notes.service`
runs the binary under systemd from `/opt/notes` (install commands in
its header). Migrations ship as files in `db/`: apply them with `lidza db
migrate` in the deploy step or `DB_MIGRATE=true` at start. Put a
TLS-terminating proxy in front, forward `X-Forwarded-For`, point the
orchestrator at `/healthz` and `/readyz`, scrape `/metrics`. Production
settings: `LIDZA_MODE` unset, `LIDZA_LOG=json`, `AUTH_COOKIE_SECURE=true`,
`AUTH_SECRET` the same on every node. The framework's `docs/deploy.md`
has the details.

## Environment the binary reads

| Variable | Meaning | Default |
|---|---|---|
| `LIDZA_ADDR` | listen address | `127.0.0.1:3000` |
| `LIDZA_MODE` | `dev` proxies the frontend instead of serving the embedded build | unset (production) |
| `LIDZA_FRONTEND_URL` | dev server to proxy to; set by `lidza dev` | |
| `LIDZA_SSR` | `1` starts the Node SSR sidecar from `dist/.server` (react template) | unset |
| `LIDZA_MCP_TOKEN` | enables `/mcp` (the app's tools over Streamable HTTP) for clients sending it as a bearer token | unset (endpoint off) |
| `LIDZA_LOG` | log format, `json` or `text` | `text` under `lidza dev` and `lidza test`, else `json` |
| `LIDZA_LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` |
