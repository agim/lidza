# Recipes for notes

Each recipe is a task, step by step. The framework's are under "Recipes" and
follow the release (`lidza gen` rewrites them); this app's own are under
"App recipes" (`lidza recipe add`, MCP `lidza_recipe_add`). Every recipe is also
a skill (`.claude/skills/<name>`, `.agents/skills/<name>`) and a `lidza mcp`
prompt: load the one the task needs rather than this file.

## Recipes

Step-by-step tasks. Each one is also a prompt in `lidza mcp`, a skill in
`.claude/skills/<name>` (Claude Code) and `.agents/skills/<name>` (Codex
and Gemini CLI); `lidza gen` rewrites them from
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

2. Register the handler in `app/routes.go`:

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
   client method (`createThing` gives `api.createThing`). A list that
   takes filters reads them with `req.Query("q")`; the client sends them
   as `{ query: { q, limit } }`. A file upload takes `router.File` as
   `In` (recipe "Store a file").

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

   Routes that share middleware go on a group:
   `g := r.Group("/api/v1/things", auth.Require())`, then
   `router.Route(g, "GET /api/v1/things/recent", recentThings)` with
   the full pattern. The prefix may hold wildcards
   (`r.Group("/api/v1/things/{id}")`, read with `req.Param("id")`), and
   every pattern of the group lies under it (another panics). Groups
   do not hide routes: all patterns share one ServeMux, so the most
   specific wins wherever it was registered (`GET
   /api/v1/things/recent` in a group over `GET /api/v1/things/{slug}`
   on `r`), two that conflict panic at start, and the group's
   middleware runs for its own routes only (a path no route matches is
   a 404 before it).

3. Save. `lidza dev` regenerates `schema/`, rebuilds, and rewrites the
   client (without it: `lidza gen`, MCP `lidza_gen`). Check with
   `curl -s -X POST http://127.0.0.1:3000/api/v1/things -d '{"title":"x"}'`.

4. Call it from the frontend as `api.createThing({ title })` from
   `@lidza/client`; never `fetch` by hand (`lidza check` warns, L003).
   A stream's method returns an async iterable: `for await (const piece
   of api.draftThing({ title }, { signal })) text += piece`; leaving the
   loop or aborting the signal closes the request. The Dart client
   returns a `Stream`.

5. Add a test in `tests/routes_test.go` (see "Write a test") and run
   `lidza check`, then `lidza test`.

### Add a resource

Give a model the five standard routes (list, get, create, patch, delete)
backed by Postgres. Needs the `db` and `auth` packs (`lidza pack add db`,
`lidza pack add auth`).

1. Declare a `model` in `schema.lidza`. A post belongs to its author:

   ```
   model Post {
     id        uuid     @id @default(uuid())
     ownerId   uuid     @index
     title     string   @min(1) @max(200)
     body      string?
     createdAt time     @default(now())
   }
   ```

   The model is owned when it has an owner field, the first of: a field
   `ownerId`, a field with `@ref(User)` or `@ref(AuthUser)` (such as
   `userId uuid @ref(User)`; a `userId` without the reference is not
   the owner). The owner is `uuid` or `string` and required: it holds
   the signed-in user's id. A model whose rows every signed-in user
   shares (a team's projects, a membership table) is marked `@shared`
   (`model Project @shared {`): behind sign-in, not scoped. One whose
   rows belong to no one and that visitors may change (rare) is marked
   `@public` (`model Tag @public {`).
2. Run `lidza gen resource Post` (MCP: `lidza_gen_resource` with
   `model: "Post"`): it writes `db/queries/post.sql`, the
   `CreatePost`, `UpdatePost` and `PostList` types in `schema.lidza`,
   `handlers/post.go` with the routes under `/api/v1/posts`, and in
   `app/routes.go` a group behind `auth.Require()` with the routes on it:

   ```go
   posts := r.Group("/api/v1/posts", auth.Require())
   handlers.PostRoutes(posts)
   ```

   For an owned model every statement filters by `owner_id` (list,
   count, get, update, delete), create sets it from
   `auth.CurrentUser(ctx).ID`, the `Create` and `Update` types leave it
   out, and another user's row is a 404. `--public` (MCP: `public: true`)
   marks the model `@public`: no owner, routes open to visitors.
   `--shared` (MCP: `shared: true`) marks it `@shared`: behind sign-in,
   rows not scoped to one user.
3. Run `lidza db migrate` (MCP: `lidza_db_migrate`) to apply the new
   migration in `db/migrations/`.
4. The generated files are ordinary code: add filters or routes there.
   `lidza check` flags a query on an owned table that neither filters by
   nor sets the owner (L018); a query meant to cross users (an admin
   page) takes `-- lidza:ignore L018` on the line before its
   `-- name:`. Regenerate with `--force` to reset the handlers (files
   generated before owners were scoped keep their old code until then).
5. `lidza check`, then `lidza test`: a second user lists nothing and
   gets 404 on the first user's id.

### Add sign-in

Give the app accounts through the auth pack's own sign-in: email and
password by default, sign-in providers (Google, GitHub, Microsoft, any
OIDC issuer) when configured. The app writes no login handler and no
OAuth flow.

1. `lidza pack add auth` (MCP: `lidza_pack_add`; needs `db`, and `mail`
   for the verification and reset links). In `app/routes.go`:
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
   `api.authSession()` once (`user` is null for a visitor). A "remember
   me" box sends `remember` to `authLogin` or `authRegister`: `false`
   makes the auth cookies session cookies (no expiry, on this reply and
   every renewal), so the session ends when the browser closes; absent
   or `true` keeps them for `AUTH_REFRESH_TTL`. Render one
   button per entry of `api.authProviders()` as a plain link to its
   `url` (add `?redirect=/path` to land elsewhere than `/`, and
   `remember=false` for a session that ends with the browser); the callback
   sets the cookies and redirects, or lands on `/login?error=...`. The
   pages `/verify` and `/reset` read `?token=` and call `authVerify` or
   `authReset`. While `me.verified` is false, a banner offers
   `authVerifyResend()`: a new link to the signed-in user's address
   (nothing is sent once it is verified, or with `NoVerifyEmail`).
   Two-factor endpoints are mounted too: start enrollment with
   `authTwoFactorSetup`, show its local `qrCodeDataUrl` for scanning and keep
   `otpauthUrl` and `secret` as fallbacks, then confirm with
   `authTwoFactorEnable({code})`, and show the returned recovery codes
   exactly once. Status is `authTwoFactorStatus`; disabling or regenerating
   recovery codes requires a current code. Once enabled, password login
   sends the current code in `X-Lidza-OTP`; provider sign-in relies on the
   provider's own second factor. Factors need `LIDZA_MASTER_KEY` so their
   secrets are sealed in the database.
   For a credential or other sensitive value, post `{code, purpose, target}`
   to `authStepUp`; the grant is bound to this account and browser session,
   that exact purpose and target, for at most 15 minutes. The server route
   must call `auth.AuthorizeStepUp(ctx, req.Raw, purpose, target)` immediately
   before returning the value. Its `OnEvent` entries
   (`EventStepUpGranted`, `EventStepUpAuthorized`, `EventStepUpRejected`)
   carry purpose and target, never the value.
3. Providers: `AUTH_PROVIDERS=google,github` in `.env` or on the admin
   pages (section "Sign-in providers"); the client id and secret in the
   app's settings: `lidza settings set AUTH_GOOGLE_CLIENT_ID=...
   AUTH_GOOGLE_CLIENT_SECRET=...`. Register the callback
   `<APP_URL>/api/v1/auth/google/callback` with the provider. Microsoft
   takes `AUTH_MICROSOFT_TENANT`; another OIDC issuer takes
   `AUTH_<NAME>_ISSUER` (and `AUTH_<NAME>_LABEL`). An identity whose
   email the provider vouches for joins the local account of that
   address; the admin Users page shows how each account signs in.
   Options: `NoRegister` (invite-only; `auth.From(ctx).CreateUser` adds
   accounts), `NoLocal` (providers only), `RequireVerified`,
   `NoVerifyEmail` (registration sends no verification link), `Claims`
   (roles into the token), `AfterSignIn`. The login route has a
   throttle of its own: `AUTH_SIGNIN_RPS` and `AUTH_SIGNIN_BURST`,
   else `AUTH_LOGIN_RPS` and `AUTH_LOGIN_BURST` as for register, verify,
   forgot and reset.
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
   runs `OnSignIn(ctx, auth.SignIn{Profile, Method, Request, Remember})`
   before the session opens: claim invitations sent to the user's email
   there, or finish a join they started signed out. `s.Request` is the
   login, register or provider callback request (its address, the app's
   own cookies); `s.SetCookie` adds a cookie to the reply, sent only when
   the sign-in succeeds:

   ```go
   OnSignIn: func(ctx context.Context, s auth.SignIn) error {
   	c, err := s.Request.Cookie("pending_join")
   	if err != nil {
   		return nil
   	}
   	n, err := queries.New(db.From(ctx)).JoinTeam(ctx, queries.JoinTeamParams{Code: c.Value, UserID: s.Profile.Subject})
   	if err != nil {
   		return err
   	}
   	s.SetCookie(&http.Cookie{Name: "pending_join", Path: "/", MaxAge: -1})
   	s.SetCookie(&http.Cookie{Name: "notice", Value: fmt.Sprintf("joined-%d", n), Path: "/", MaxAge: 60})
   	return nil
   },
   ```

   An error refuses the sign-in (`?error=signin` for a provider). Token
   refreshes do not run it, and neither does `Claims`, which runs for
   every token and is for claims only.
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

   A security log: `OnEvent(ctx, auth.Event{Kind, Subject, Email,
   Method, Reason, Request})` runs after each account event the routes
   handle: `auth.EventSignedIn`, `EventSignInFailed` (Reason
   `bad_credentials`, `not_verified`, `refused`, `disabled`, or a
   provider's `denied`, `state`, `provider`, `signup`; Subject empty for
   an address no account has), `EventSignedOut`, `EventSignedUp`,
   `EventPasswordChanged`, `EventPasswordCheckFailed` (a wrong current
   password on the change or delete route), `EventResetRequested`,
   `EventPasswordReset`, `EventEmailVerified` and `EventAccountDeleted`.
   It cannot refuse anything and runs on the request: insert a row (the
   subject, the kind, `Request.RemoteAddr` or the app's client address,
   the user agent) and log your own errors.
6. Emails: with the mail pack the links go out as plain text, or
   through `mail/auth_verify.txt.tmpl` and `mail/auth_reset.txt.tmpl`
   when the app has them (Data: `App`, `Link`, `Email`, `Name`). A
   language's copy is `mail/auth_verify.<lang>.txt.tmpl` (`de`, `pt-BR`),
   chosen by the request's language (recipe "Send an email"); it names
   its subject with `{{define "subject"}}...{{end}}`.
   `NoVerifyEmail: true` sends no verification link at registration: an
   app that sends its own welcome email does so from `OnSignUp` (with
   `RequireVerified`, it must then send a link itself:
   `IssueToken(ctx, auth.PurposeVerifyEmail, s.Profile.Subject, 0)` and
   `VerifyPath?token=`, which the verify route redeems).
7. Test: `lidzatest.Start`, `POST /api/v1/auth/register`, then a scoped
   route with the cookies the reply set. The providers are tested by the
   framework (against an OIDC issuer in a test server), not by the app.
   `lidza check`, then `lidza test`.

### Scope a query to the signed-in user

Make a resource answer only with the rows its user may see: a row
another user may not read is a 404, never a 403 (the reply must not
confirm it exists).

1. Behind `auth.Require()`, take the user from the context:
   `user := auth.CurrentUser(ctx).ID`, never from the body.
2. Owned rows (one user each): give the model an owner field (`ownerId
   uuid`) and `lidza gen resource` writes the scoped queries and
   handlers (recipe "Add a resource"). By hand: `AND owner_id = $N` in
   every statement of `db/queries/<table>.sql` (list, count, get,
   update, delete), `owner_id` set from the user on create, and no
   `ownerId` in the `Create` and `Update` types. `lidza check` flags a
   query that misses it (L018). The snippets `queries` and
   `resource-handlers` are this case.
3. Shared rows (a team, a project): keep a membership table
   (`Membership @shared { projectId @ref(Project, cascade), userId
   @ref(User, cascade), role }` with `@@unique(projectId, userId)`;
   `@shared` because its rows belong to the project, not to the user
   the `userId` names) and join on it:

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
10. A public page that needs no React in the browser (text, links,
   images, a form that posts or a `mailto:`) sets `staticData: { static:
   true }` on its route: `npm run build` writes its HTML without the
   client runtime (no React, router or query code, no hydration), so a
   visitor landing on it downloads its HTML and CSS only (`lidza audit
   performance` shows the difference). It shows what it renders at build
   time: no `useQuery`, no loader that needs the API, no state. Its
   links are plain links. A little behaviour (a menu toggle, a form's
   checks) goes in `src/enhance/<name>.ts`, plain DOM code the page works
   without, named on the route: `staticData: { static: true, enhance:
   ['disclosure'] }` (`src/enhance/disclosure.ts` is the example). The
   app's pages keep hydrating as before; choose static per page. A site
   that is all content is the astro template's job.
11. Add a browser test in `e2e/things.spec.ts` (see "Write a test"), then
   `lidza check` and `lidza test --e2e`.

### Set the head of a page

Give each address its own title, description, canonical address, social
cards and structured data. Search engines and link previews read the
HTML the server sends, not what the browser renders later, and a route
with a parameter (`/products/$id`) is one shell for every address until
the server names it. The binary sets the head per request, without the
SSR sidecar.

1. In `main.go`, set `Head: head` on `lidza.App`, and write `head` in
   `head.go` (package main): `func head(r *http.Request) (lidza.Head,
   bool)`. It returns false for the pages it does not know; they keep the
   head they were built with.
2. Match the path (`id, ok := strings.CutPrefix(r.URL.Path,
   "/products/")`), load the record with one query
   (`queries.New(db.From(r.Context()))`: the packs are in the request's
   context), and fill `Title`, `Description`, `Canonical` (the absolute
   address), `Image` (absolute), `Type` (`article`, `product`; `website`
   when empty), `SiteName`, further tags in `Meta`
   (`lidza.HeadMeta{Property: "article:published_time", Content: ...}`)
   and `JSONLD` (maps or structs, each written as its own `<script
   type="application/ld+json">`).
3. A missing record: `lidza.Head{Title: "Not found", NoIndex: true,
   Status: http.StatusNotFound}`, so the address answers 404 and is not
   indexed. A draft or private page: `NoIndex: true`.
4. Pass plain text: every value is escaped where it is written, JSON-LD
   included. The title, the description, the canonical link and each tag
   replace the page's own of the same name; the rest of the page's head
   (stylesheets, scripts) stays. og:title, og:description, og:url,
   og:image and the twitter tags follow from the fields.
5. It applies to the shell, the prerendered pages, the pages the SSR
   sidecar renders and, under `lidza dev`, the dev server's. Setting
   `document.title` in the browser on navigation stays the client's job;
   the server's head is for the first load and for crawlers.
6. Test it beside the package that defines `head` (outside the root): call `head` with
   `httptest.NewRequest("GET", "/products/"+id,
   nil).WithContext(srv.Context())` (`srv` from `lidzatest.Start`) and
   check `Title`, `Canonical` and the 404 `Status`. `lidza check`, then
   `lidza test`.

### Add a responsive image

Ship each image at the size the screen needs, in AVIF or WebP, without
layout shift.

1. Put the image under `src/` (not `public/`, which is copied as it is)
   and import it with `?responsive`: `import hero from
   './hero.jpg?responsive'`. The build (`vite-imagetools`, configured in
   `vite.config.ts`) writes it at 480, 800, 1200, 1600 and 2400 pixels
   wide (those below its own width, then its own) in AVIF, WebP and the
   original format, content-hashed in `dist/assets/`, which the binary
   caches for a year. No encoder to install: `npm install` brings one.
2. Render it with `<Picture image={hero} alt="..."
   sizes="(min-width: 768px) 50vw, 100vw" />` from `src/picture.tsx`. `sizes`
   says how wide the image is drawn, so the browser downloads one
   variant that fits; the default, `100vw`, is right only for a
   full-width image. The width and height it carries keep the page from
   shifting while it loads.
3. The largest image of the first screen (the LCP element) gets
   `priority`: loaded eagerly and first. Every other image loads lazily.
   Never preload the variants by hand.
4. An app made before `vite-imagetools` was in the template: `npm i -D
   vite-imagetools`, then copy `vite.config.ts`'s `responsive` lines,
   the `?responsive` declaration in `src/vite-env.d.ts` and `src/picture.tsx` from
   a new app (`lidza new tmp --template react`).
5. Images people upload are the media pack's (resized and converted on
   upload, "Store a file"); this recipe is for the images the app ships.
6. `alt` describes the image for someone who cannot see it; an image
   that only decorates gets `alt=""`.

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

1. In `app/tools.go` add to the list returned by `tools()`:

   ```go
   lidza.ToolFunc("count_posts", "Number of posts.", func(ctx context.Context, _ struct{}) (int, error) {
   	var n int
   	err := db.From(ctx).QueryRow(ctx, "SELECT count(*) FROM post").Scan(&n)
   	return n, err
   }),
   ```

2. Say what it does to the data: `ReadOnly: true` for a lookup or a
   report, so the agent's client runs it without asking, and
   `Destructive: true` for one that deletes or overwrites. A tool marked
   neither counts as one that may destroy, and the client asks first:

   ```go
   count := lidza.ToolFunc("count_posts", "Number of posts.", countPosts)
   count.ReadOnly = true
   ```

3. Give the input a `schema.lidza` type to have its rules enforced; the
   input schema shown to the agent comes from the Go type.
4. `lidza check`; then restart `lidza mcp` (the MCP client reconnects) and
   call `app_count_posts`.

### Send an email

Send a transactional email (verification, reset, receipt) through the
mail pack, never through a vendor SDK.

1. `lidza pack add mail` (MCP: `lidza_pack_add`; after `db`, and `jobs`
   for background delivery);
   set `MAIL_FROM` and, in production, `MAIL_PROVIDER` with its key in
   `.env`. `.env.test` gets `MAIL_PROVIDER=outbox`. Click and open
   tracking stay off on Mailgun, SendGrid and Postmark whatever the
   account says (a verification link is never rewritten to a tracker);
   `MAIL_TRACKING=true` hands it back to the account.
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
   jobs pack, or delivered right away without it). The outbox preserves
   `From`, `ReplyTo`, `Cc`, `Bcc`, `Attachments` and custom `Headers` for delivery and retries. Do not build the
   message with `fmt.Sprintf` and do not call the vendor's API.

   When mail follows a database write, use `SendTx(ctx, tx, message)`
   inside that write's transaction. It requires both `db` and `jobs`,
   using the same database as `tx`; it stores the outbox row and delivery
   job together. The caller commits or rolls back, and must roll back on
   any error. Do not call ordinary `Send` inside a transaction: it uses
   its own connection and transaction. Workers cannot see a queued
   message until commit. Delivery retains the jobs pack's retries;
   this does not guarantee exactly-once delivery at the provider.

   ```go
   _, err := mail.From(ctx).SendTx(ctx, tx, mail.Message{
       To: user.Email, Subject: "Your receipt", Template: "receipt", Data: receipt,
   })
   if err != nil {
       return err // the caller rolls back tx, including its earlier writes
   }
   return tx.Commit(ctx)
   ```

   `To` also accepts a comma-separated address list. `Cc` and `Bcc` are
   slices of individual addresses; display names are allowed. Each address
   must appear only once across the three lists. Blind recipients are
   never included in SMTP headers. Attach files as `mail.Attachment{Name:
   "receipt.pdf", ContentType: "application/pdf", Data: pdfBytes}`; pass
   bytes, never a path or URL. An empty content type is detected from the
   bytes. Attachments and recipients survive queued delivery and retries.
   For embedded HTML files, set `ContentID: "logo@message"` on the attachment
   and reference it as `cid:logo@message` in HTML. IDs are unique, at most
   127 bytes of printable ASCII, without whitespace, angle brackets,
   parentheses, square brackets, backslashes, commas, colons, semicolons or
   double quotes. Pass the bare ID, without `cid:`. Inline and ordinary files
   share the attachment limits; inline files require an HTML body.
   `ReplyTo` accepts a comma-separated address list, bounded independently by
   `MAIL_MAX_RECIPIENTS`, with no duplicate addresses. Custom header names
   are unique ignoring case; the pack refuses ambiguous duplicates.
   Defaults: `MAIL_MAX_RECIPIENTS=50`, `MAIL_MAX_ATTACHMENTS=10`,
   `MAIL_MAX_ATTACHMENT_BYTES=10485760` (total decoded bytes), and
   `MAIL_SMTP_TIMEOUT=30s` for the entire SMTP exchange.
   Custom `Headers` cannot override address, subject or MIME headers;
   use the message fields. Header values must not contain control characters.
4. Test it: `mail.From(srv.Context()).WaitFor(ctx, to, "Verify", 5*time.Second)`
   returns the newest message to that address whose subject contains the
   text, waiting for one a job sends; `Outbox(ctx, 5)` lists them newest
   first. Both have `Status`, `Text` and `HTML`; read the link out of the
   text. The snippet `auth-handlers` and `tests/routes_test.go` in the
   reference app show both sides. For `SendTx`, also assert that rolling
   back leaves neither the app write, the outbox row nor the delivery job.
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
4. Register it in `app/start.go`:

   ```go
   jobs.FromServices(s).Handle("notify.task", handlers.NotifyTask)
   ```

   A kind that must not overlap with itself (a sync against one outside
   account, a rebuild of one index) takes a cap that holds across every
   node: `Handle("catalog.sync", handlers.CatalogSync,
   jobs.Concurrency(1))`. Jobs over the cap wait as pending while the
   workers run other kinds; no advisory lock in the handler. Every node
   that handles the kind declares the same cap.
5. Enqueue from the handler that caused it: `jobs.From(ctx).Enqueue(ctx,
   "notify.task", schema.NotifyTask{...})`, or inside a transaction
   `EnqueueTx(ctx, tx, ...)` so the job is committed with the write and
   never runs for one that rolled back. `jobs.RunAt(t)` delays one run.
   Never do the work in the request as a fallback. When many requests
   can ask for the same work (every view of a post that lacks its
   thumbnails), give it a key: `Enqueue(ctx, "post.thumbnails", p,
   jobs.Unique("post:"+id))` stores one job while one with that kind and
   key is pending or running, on any node, and returns that job's id;
   `jobs.Existed(&found)` says whether it was already there. Once the
   job is done or failed, the key queues new work again.
6. Work on a timetable (a weekly digest, a sync every 15 minutes) is a
   schedule in `app/start.go`, not a job that enqueues its next run:

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
   clocks change; `jobs.DailyAt(zone, "02:00", "10:00", "18:00")` runs
   at several clock times a day. The payload (here `nil`) goes with every run. However
   many nodes run, one job is enqueued per due time; after downtime one
   missed run is enqueued, not one per missed time. The `job_schedule`
   table, like the job table's `uniqueKey` column, comes from
   `schema.lidza`: `lidza gen`, then `lidza db migrate`. The admin Jobs page lists each schedule with its next and
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
   `Authorize` no topic can be subscribed (`realtime.AllowAll` opens
   every topic to every connection, for topics that carry nothing
   private):

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
7. **Live-update acceptance gate:** add regression fixtures for repeated updates,
   slow and failed reads, short and sustained reconnects, and pause/resume.
   Preserve current values during background reads, coalesce notifications
   without repeatedly cancelling a slow read, and keep stable DOM identity,
   panel order and reserved geometry, chart series identity/colors, scroll,
   focused input and editor drafts. New data must actually appear; initial
   loading and genuine failures remain visible. Keep query scopes separate.
   Render on both desktop and phone and observe the whole refresh interval for
   transient fetch/reconnect text; a successful refresh or final screenshot
   alone is not sufficient. Check the socket after the ordinary API deadline.
8. `lidza check`, `lidza test`, and `lidza test --e2e` with these fixtures before
   calling the live UI ready. Respect explicit browser restrictions: when
   rendered checks cannot run, record and report them as unverified. Nonbrowser
   checks can proceed, but do not silently waive this acceptance gate.

### Add an LLM feature

Put a language model behind an API route: a chat reply, a summary, a
structured extraction into a schema type, or a model that uses the
app's tools. The llm pack speaks the providers; the app never does.

1. `lidza pack add llm` (MCP: `lidza_pack_add`). `.env` gets
   `LLM_PROVIDER=fake`; set `ollama` for a local model, or `anthropic`,
   `openai` or `google` with `LLM_API_KEY` and, when the default does not
   suit, `LLM_MODEL` (on the admin Language model page the field lists
   the provider's models for the key and refuses a name it does not
   have). `.env.test` keeps `fake`. Try the setup with the
   MCP tool `lidza_llm` before writing code. Embeddings come from the
   chat provider unless `EMBED_PROVIDER` names another: `openai`,
   `google`, `ollama` or `compatible`, with `EMBED_API_KEY`,
   `EMBED_MODEL` and `EMBED_BASE_URL` as needed. Anthropic has no
   embedding model, so an Anthropic app that embeds sets it;
   `EMBED_PROVIDER=none` turns embeddings off. Keys go in the
   app's settings (`lidza settings set EMBED_API_KEY=...`).
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
   `llm-handler` and `tests/routes_test.go` in the reference app show it. The
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
2. Accept the upload with a typed route whose input is `router.File`
   (the body is the file itself, not JSON):

   ```go
   router.Route(g, "PUT /api/v1/posts/{id}/image", uploadImage, router.UploadLimit(10<<20))

   func uploadImage(ctx context.Context, req *router.Request[router.File]) (router.None, error) {
   	// the same access check as the post first
   	_, err := storage.From(ctx).Put(ctx, "posts/"+req.Param("id")+"/image", req.Body.Body,
   		storage.PutOptions{ContentType: req.Body.ContentType})
   	return router.None{}, err
   }
   ```

   `req.Body.Body` reads the bytes, bounded by `router.UploadLimit`
   (32 MB, `router.MaxUploadBytes`, without it): a larger body replies
   413. `req.Body.ContentType` and `req.Body.Name` are what the client
   sent; the name is never a key or a path. Store under a key that
   names the owner and the row (`posts/<id>/image`) and keep it in a
   column of the row when it does not follow from the id. The
   generated client takes the File or Blob after the path parameters:
   `await api.uploadImage({ id }, file, { onProgress: (sent, total) =>
   setShare(sent / total) })` (the File's type becomes the
   Content-Type, `contentType` overrides it; `onProgress` is optional
   and browser-only); the Dart client takes the bytes, `contentType`
   and `filename`. No hand-written `fetch`.
3. Read it back through the app (`storage.Handler` or a handler that
   checks access and streams `Get`), or hand the browser a
   `PresignGet` URL for a minute; never a permanent public URL for a
   private file. Delete the object when the row goes.
   Public files (served through a CDN) go in a store of their own, so
   they never share a bucket and prefix with private ones:
   `STORAGE_STORES=media`, `STORAGE_MEDIA_BUCKET`, `STORAGE_MEDIA_PUBLIC_URL`
   (the CDN's address), and only the settings that differ from the main
   store (`STORAGE_MEDIA_REGION`, `_ACCESS_KEY`, ...). In code,
   `storage.Named(ctx, "media")` has the same methods; `URL(key)` is the
   CDN address. After replacing public files, empty the CDN's cache:
   `storage.Named(ctx, "media").Purge(ctx, "sites/42/")` (a trailing
   slash purges the folder) with `STORAGE_MEDIA_CDN=digitalocean`,
   `STORAGE_MEDIA_CDN_ENDPOINT_ID` and `STORAGE_CDN_TOKEN` in the
   credentials; the local provider ignores it. `PresignDelete` hands a
   worker outside the app a URL that deletes one object. Before trusting
   a bucket with private files (backups, exports), check it:
   `storage.From(ctx).Privacy(ctx, key)` asks, with no credentials,
   for the object, a listing and the public URL, and reports each check
   as `denied`, `public` or `inconclusive` (`Private` only when all are
   denied); `PrivacyProbe(ctx, "backups/probe.dump")` does it with a
   temporary object named like the real ones.
4. Test with the local provider: `.env.test` sets `STORAGE_PROVIDER=local`
   and a `STORAGE_DIR`; upload through the API, read it back, delete the
   row and check `Stat` returns `storage.ErrNotFound`. The snippet
   `storage-handler` and `tests/routes_test.go` in the reference app show it.
5. `lidza check`, then `lidza test`.

### Receive a webhook

Take a provider's deliveries (a payment provider's events, a mail
provider's bounces) on a route that verifies the signature on the raw
body, refuses replays and handles each delivery once, with
`pkg/webhook`. Never decode the body or trust a field before it is
verified.

1. Seal the signing secret for each mode: `lidza credentials set
   dev.PAYMENTS_WEBHOOK_SECRET=whsec_... production.PAYMENTS_WEBHOOK_SECRET=whsec_...`
   (the sandbox endpoint's secret for `lidza dev`, the live one for
   production). Without the setting every delivery is refused with 503
   and the log names it; an endpoint never runs unverified. A value
   saved from the admin pages applies within a minute.
2. Register the endpoint in `app/routes.go`, on `r` and not in a group
   behind `auth.Require()` (the provider has no session):

   ```go
   r.Handle("POST /api/v1/webhooks/payments", webhook.Stripe("PAYMENTS_WEBHOOK_SECRET", handlers.PaymentEvent))
   r.Handle("POST /api/v1/webhooks/mail", webhook.Mailgun("MAIL_WEBHOOK_SIGNING_KEY", handlers.MailEvent))
   ```

   Other providers: `webhook.Standard("NAME", h)` for the Standard
   Webhooks headers (`webhook-id`, `webhook-timestamp`,
   `webhook-signature`, a `whsec_` secret) that Svix, Resend and another
   Līdza app's hooks pack send; `webhook.HMAC("NAME", "X-Hub-Signature-256", h,
   webhook.Prefix("sha256="), webhook.IDHeader("X-GitHub-Delivery"))`
   for an HMAC-SHA256 of the body in a header (`webhook.Base64()` when
   it is base64); `webhook.Token("NAME", "Authorization", h,
   webhook.Prefix("Bearer "))` or `webhook.TokenField("NAME",
   "auth.token", h)` for a shared token in a header or a JSON field.
   A token does not cover the body: use it only when the provider
   offers nothing better. Stripe and Mailgun sign a timestamp, and a
   delivery more than 5 minutes off is refused (`webhook.Tolerance`);
   the body is capped at 1 MB (`webhook.MaxBody`, raise it for inbound
   mail with attachments) and the handler has 10 seconds
   (`webhook.Timeout`).
3. Write the handler in `handlers/webhooks.go`:

   ```go
   func PaymentEvent(ctx context.Context, d *webhook.Delivery) error {
   	ev, err := d.StripeEvent()
   	if err != nil {
   		return err
   	}
   	switch ev.Type {
   	case "checkout.session.completed":
   		var s struct {
   			ID        string `json:"id"`
   			Reference string `json:"client_reference_id"`
   		}
   		if err := json.Unmarshal(ev.Data.Object, &s); err != nil {
   			return err
   		}
   		return jobs.From(ctx).Enqueue(ctx, "order.paid", schema.OrderPaid{OrderID: s.Reference})
   	}
   	return nil // events the app ignores are acknowledged too
   }
   ```

   `d.Body` is the verified body, `d.Decode(&v)` decodes it,
   `d.MailgunEvent()` reads a mail event (`Event`, `Recipient`,
   `Severity`), `d.Form` holds a form post's fields. Returning nil
   replies 200; an error replies 500 and the provider retries. Keep
   the handler short and put slow work in a job.
4. Each delivery id (the event id, the mail token, the `IDHeader`) is
   handled once: a repeat replies 200 without calling the handler, and
   an id whose handler failed is handled again on the retry. The ids
   live in the `webhook_delivery` table when the db pack runs, kept
   for 30 days (`webhook.Retention`); with several nodes the db pack is
   required. Without it, dev and test keep them in memory and
   production refuses (503) unless `webhook.WithStore` names a store.
   The provider may still send two events about the same object, out
   of order: write with upserts keyed by the object's id.
5. Test it in `tests/routes_test.go`, signing as the provider does:

   ```go
   func TestPaymentWebhook(t *testing.T) {
   	t.Setenv("PAYMENTS_WEBHOOK_SECRET", "whsec_test")
       srv := lidzatest.Start(t, app.New(nil))
   	body := `{"id":"evt_1","type":"checkout.session.completed","data":{"object":{"id":"cs_1","client_reference_id":"42"}}}`
   	sig := webhook.StripeSignature("whsec_test", srv.Clock.Now(), []byte(body))
   	res := srv.JSON(t, "POST", "/api/v1/webhooks/payments", json.RawMessage(body), nil, lidzatest.Header("Stripe-Signature", sig))
   	if res.StatusCode != http.StatusOK {
   		t.Fatal(res.StatusCode)
   	}
   }
   ```

   Keep the body compact and free of `<`, `>` and `&`: `srv.JSON`
   compacts and escapes a `json.RawMessage`, and the signature covers
   the exact bytes. `srv.Clock.Now()` signs at the app's time, so a
   test that moves the clock (`srv.Clock.Advance(10*time.Minute)`)
   sees an old delivery refused. Also check a wrong secret gets 401. `webhook.MailgunSignature(key, t, token)`
   builds the mail provider's `signature` object, `webhook.HMACSignature`
   an HMAC header's value.
6. By hand against `lidza dev`, sign with the dev secret:

   ```sh
   body='{"id":"evt_local_1","type":"ping"}'; t=$(date +%s)
   sig=$(printf '%s.%s' "$t" "$body" | openssl dgst -sha256 -hmac "whsec_..." -r | cut -d' ' -f1)
   curl -si http://127.0.0.1:3000/api/v1/webhooks/payments -H "Stripe-Signature: t=$t,v1=$sig" -d "$body"
   ```

   A provider's CLI that forwards its sandbox events (`stripe listen
   --forward-to 127.0.0.1:3000/api/v1/webhooks/payments`) prints the
   secret to seal as `dev.PAYMENTS_WEBHOOK_SECRET`.
7. `lidza check`, then `lidza test`.

### Send webhooks

Let the app's users (or other systems) subscribe a URL to its events:
each event POSTed there, signed, retried until it lands, with a log.

1. `lidza pack add hooks` (MCP: `lidza_pack_add`; after `db` and
   `jobs`), then `lidza gen` and `lidza db migrate` (`hook_endpoint`,
   `hook_delivery`). Endpoint secrets are sealed with the master key.
2. Send from a handler or a job, once the change is made:
   `n, err := hooks.From(ctx).Send(ctx, owner, "order.paid", order)`,
   or `SendTx(ctx, tx, owner, "order.paid", order)` inside the change's
   transaction, so a rolled-back change sends nothing. `owner` is whose
   endpoints receive it: the workspace (`auth.WorkspaceID(ctx)`), the
   user, or `""` for the app's own. Event names are lower case with
   dots (`order.paid`); the body is `{"type", "timestamp", "data"}`,
   the same bytes on every attempt.
3. Let users manage their endpoints: `hooks.Mount(r, hooks.Options{})`
   in `app/routes.go` serves `/api/v1/hooks` (create, list, change,
   delete, reveal and rotate the secret, ping, the deliveries, replay),
   owned by the request's workspace, else the user;
   `Options{Guard: auth.RequireWorkspace()}` or a permission
   (`roles.Require("hooks.manage", ...)`) to restrict it. The client
   gets `api.hooksList`, `api.hookCreate` (its reply carries the secret
   once), `api.hookPing`, `api.hookDeliveries`, `api.hookReplay`. An
   endpoint takes every event, or the ones it names (`order.*`).
4. Delivery: signed with Standard Webhooks headers (`webhook-id`,
   `webhook-timestamp`, `webhook-signature`), which receivers verify with
   any Standard Webhooks library or, in a Līdza app,
   `webhook.Standard("THEIR_SECRET", handler)`; retried at 5 s, 30 s, 2
   min, 10 min, 30 min, 1 h, 3 h and 6 h; redirects not followed; after
   `HOOKS_DISABLE_AFTER` deliveries that all failed, the endpoint is
   disabled until its owner or the admin pages enable it. URLs on
   internal addresses (loopback, private ranges, link-local, the cloud
   metadata address) are refused when saved and at each connection;
   `HOOKS_ALLOW_PRIVATE=true` allows them for development only.
5. The admin pages' Webhooks list every endpoint and the latest
   deliveries, with Replay and Enable.
6. Test with an `httptest` receiver: set `HOOKS_ALLOW_PRIVATE=true` in
   `.env.test`, create an endpoint for its URL, send, and check what it
   received and verified.

### Connect an external account

Let a signed-in user connect an account of another service (a code
host's repositories, a calendar, a storage service) so the server calls
its API as that user. This is not sign-in: the account opens no
session. The auth pack keeps the grant, its tokens sealed with the
master key in `auth_connection`, renews it, and never sends a token to
the browser.

1. Register an OAuth app with the provider. For GitHub: an OAuth App
   whose callback URL is `<APP_URL>/api/v1/auth` (GitHub accepts any
   redirect under it, so it serves sign-in with GitHub too) or exactly
   `<APP_URL>/api/v1/auth/connect/github/callback`. Seal its keys per
   mode and name it: `lidza credentials set
   dev.AUTH_CONNECT_GITHUB_CLIENT_ID=... dev.AUTH_CONNECT_GITHUB_CLIENT_SECRET=...`
   (the production app's under `production.`), and `AUTH_CONNECT=github`
   in `.env` and `lidza.json`'s `deploy.env`. The default scopes are
   `repo admin:repo_hook` (repositories, private ones included, and
   their webhooks); `AUTH_CONNECT_GITHUB_SCOPES` asks for others. A
   grant missing a scope is refused and withdrawn.
2. Other providers go in the options, in `app/routes.go`:

   ```go
   auth.Mount(r, auth.Options{Connectors: []auth.Connector{
   	auth.GitHubConnect(id, secret),
   	auth.OAuth2Connect(auth.OAuth2Config{Name: "calendar", ClientID: cid, ClientSecret: csecret,
   		AuthURL: "https://provider.example/oauth/authorize", TokenURL: "https://provider.example/oauth/token",
   		RevokeURL: "https://provider.example/oauth/revoke", UserURL: "https://provider.example/oauth/userinfo",
   		Scopes: []string{"calendar.read"}, PKCE: true}),
   }})
   ```

   `Options.ConnectAuthorize` may refuse a user starting one (a plan,
   a workspace role).
3. The page links to `/api/v1/auth/connect/github/start?redirect=/settings`
   (a plain link, not a fetch: the browser goes to the provider and
   back). It lands on the redirect with `?connected=github`, or with
   `?connect_error=<reason>&provider=github` (denied, state, scopes,
   refused, provider). `GET /api/v1/auth/connections` lists the user's
   connections (provider, account, scopes, `reconnect` when the grant
   stopped working), never a token; `DELETE
   /api/v1/auth/connections/github` revokes the grant at the provider
   and forgets it.
4. On the server, call the API as the user:

   ```go
   conn, err := auth.From(ctx).Connection(ctx, auth.CurrentUser(ctx).ID, "github")
   if errors.Is(err, auth.ErrNotConnected) || errors.Is(err, auth.ErrReconnect) {
   	return out, router.Errorf(http.StatusConflict, "connect GitHub first")
   }
   res, err := conn.Client().Get("https://api.github.com/user/repos")
   ```

   `Client` sends the token and renews an expiring one (at most 8
   renewals at once per node, 15 seconds each; concurrent callers
   share one). A refused renewal is `auth.ErrReconnect`: the user
   connects again. The owner is always the app's decision: the
   signed-in user, or one an app's own check allows (a workspace's
   connection used by its members). A job takes the owner in its
   payload, never a token.
5. A GitHub App installation token is the narrower alternative for
   repositories (chosen repositories, fine-grained permissions, an hour
   each): the app mints those itself; they are not a user's connection.
6. `lidza gen` (and so `lidza update`) adds the `AuthConnection` model
   to an app from before connections; then `lidza db migrate`.
   Test with a fake provider (`httptest.NewServer`) and
   `auth.OAuth2Connect` pointed at it.

### Add a paginated, filterable list

Give a list search, filters, a date range, sorting and pages, so no page
shows an endless table.

1. `lidza gen resource <Model>` writes it for a model: the list route
   reads its parameters with `list.Read` (`pkg/list`) and the queries
   search the model's text fields, filter on its enums, booleans and
   references, bound its time (`createdAt` first), sort by its plain
   fields (newest first by default), and count the same rows. For a
   list of your own, the same: `p, err := list.Read(req,
   list.Options{Sorts: []string{"createdAt", "title"}, Desc: true,
   Filters: []string{"status"}})` (an unknown sort or a bad date is a
   422 naming the parameter), and the SQL in `db/queries/<table>.sql`
   as the generator writes it: `sqlc.narg('q')` and the filters `NULL`
   for none, `position(lower(...))` for the search (a `%` is just a
   character), one `CASE` per sort key in `ORDER BY`, `LIMIT
   sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int`, and a `Count*`
   query with the same conditions.
2. The query string: `limit` (50, at most 200), `offset`, `q`, `since`
   and `until` (instants; until is exclusive), `sort`, `order` (`asc`,
   `desc`), and one parameter per filter.
3. The page (react template), from `src/list.tsx`: `useListState({
   filters: ['status'] })` keeps the state in the address,
   `api.listPosts({ query: listQuery(state) })` sends it (the date
   inputs' days become instants in the visitor's zone, until the end of
   the last day), and `ListToolbar` (the search, the range labelled by
   its field, the filters in one row), `FilterSelect` ("Status: Draft"),
   `SortHead` and `Pager` ("51–100 of 230") draw it. A list already in
   the browser pages with `pageRows(rows, state)`.
4. Full-text search: mark the fields to search `@search` in
   `schema.lidza` (`title string @search`, `body text? @search`, and
   `@@search("english")` for stemming) and run `lidza gen`: the
   migration adds a GIN index on their weighted text. `lidza gen
   resource` then searches with it instead of `position()`: words in any
   form ("running" finds "run"), `"quoted phrases"`, `or`, `-excluded`
   (websearch syntax); a search without a chosen sort is ranked by
   relevance (`sort=relevance`), title matches before body matches; and
   the reply's `highlights` (`[{id, parts: [{text, hit}]}]`, for the
   page's rows) mark the matched words. Show them with `<Highlight
   parts={highlightOf(posts.data, post.id)} fallback={post.title} />`
   from `src/list.tsx`: the hits in `<mark>`, everything escaped. For a
   query of your own, use the model's expression from the generated
   `db/queries/<table>.sql` unchanged, or the index is not used.
5. Test the route with `lidzatest`: a search, a filter, a range, a sort
   both ways and the last page, and a 422 for an unknown sort.

### Dates and time zones

Store instants in UTC, keep calendar days as days, and show both in the
visitor's language and zone.

1. Pick the field type in `schema.lidza`:
   - `time` is an instant (`timestamptz`): a post's `createdAt`, a
     meeting's start. JSON carries it in UTC, `"2026-10-06T14:30:00Z"`.
   - `date` is a calendar day with no zone (`date`, Go `civil.Date`): a
     birthday, a due date, a booking night. JSON carries
     `"2026-10-06"`, which no client shifts to another day.
   - `localtime` (API types only) is a date and time as a person typed
     it, with no zone: what `<input type="datetime-local">` sends,
     `"2026-10-06T10:30"`. The handler turns it into the instant:

     ```go
     start, skipped := i18n.From(ctx).At(ctx, req.Body.StartAt)
     if skipped { // 02:30 on the night clocks jump to 03:00 is 03:30
     	return out, router.Errorf(http.StatusUnprocessableEntity, "that time does not exist there; it would be %s", i18n.From(ctx).Time(ctx, start))
     }
     ```

2. The zone is the request's: the `i18n` pack reads the `tz` cookie the
   page sets from the browser, or `X-Timezone` from other clients, with
   `I18N_TIMEZONE` as the default. An app that stores a user's zone (a
   `zone string @timezone` field on its profile) applies it in its own
   middleware after sign-in, `ctx = i18n.WithTimezone(ctx, loc)` (with
   `loc, _ := time.LoadLocation(profile.Zone)`), and jobs and mail do the
   same with the user's stored zone, since they run outside a request.
   The zone data is built into the binary.
3. On the server: `i18n.From(ctx).Today(ctx)` is today in that zone,
   `Date`, `Time` and `DateTime` format an instant in it, `Relative` says
   "3 hours ago". `civil.Date` has `AddDays`, `DaysUntil`, `Before`,
   `In(loc)` (the start of that day there); compare instants as
   `time.Time` in UTC, and read the time with `lidza.Now(ctx)` so a test
   can set it.
4. In pages, `src/datetime.ts`: `formatDate`, `formatTime`,
   `formatDateTime`, `formatRelative` in the page's language and the
   visitor's zone (`setTimeZone(user.zone)` for a stored one),
   `toLocalInput(instant)` to fill a datetime-local input, `today()` for
   a date input. Format after the page loads, never while it is
   prerendered.
5. Test the edges with a fixed clock (`srv.Clock` in `lidzatest`) and a
   zone with daylight saving (`America/New_York`): the day after 23:00,
   the night the clocks change, a date near midnight UTC.

### Roles and permissions

Let the app decide who may do what, per team, beyond signed in or not.
The roles and their permissions are the app's, in code; who holds a
role, and in which team, is in the auth pack's `auth_member` table.

1. Define the roles once, in the app package:

   ```go
   var roles = auth.Roles{
   	"admin":  {"*"},
   	"editor": {"posts.*", "members.read"},
   	"viewer": {"posts.read", "members.read"},
   }
   ```

   A permission is a name (`posts.write`), `prefix.*` for every name
   under it, or `*`. A role the map no longer names grants nothing.
2. Guard routes with a permission in the request's scope (a team, a
   workspace; whatever the app groups by):

   ```go
   team := func(r *http.Request) string { return r.PathValue("team") }
   g := r.Group("/api/v1/teams/{team}/posts", roles.Require("posts.read", team))
   router.Route(r, "POST /api/v1/teams/{team}/posts", createPost, roles.Require("posts.write", team))
   ```

   `Require` signs the request in like `auth.Require()` and replies 401
   without a session, 403 without the permission, 500 when the check
   cannot run: it never lets a request through on an error. Inside a
   handler or a job, `roles.Check(ctx, team, "posts.publish")` returns
   nil, `auth.ErrForbidden` (a 403 from a typed handler) or the error.
3. Grant and revoke from the app's own team routes, guarded by a
   permission of their own (`members.manage`):

   ```go
   err := roles.Grant(ctx, subject, team, "editor") // idempotent; the granter is recorded
   err = roles.Revoke(ctx, subject, team, "editor")
   err = roles.RevokeAll(ctx, subject, team)          // a member leaving the team
   ```

   `auth.AppWide` ("") as the scope holds in every team: the app's own
   operators. `roles.Of(ctx, subject, team)` lists a user's roles there,
   `roles.Scopes(ctx, subject)` the teams they belong to,
   `roles.Members(ctx, team, cursor, limit)` a team's members a page at
   a time.
4. Every check reads the database: a revoked role, or a deleted
   account (whose memberships go with it), is refused on its next
   request even with a session still valid. Keep the first operator in
   `ADMIN_USERS` or grant them `admin` app-wide at setup.
5. `lidza gen` (and so `lidza update`) adds the `AuthMember` model to an
   app from before roles; then `lidza db migrate`. Test each role
   against each route with `lidzatest`: a viewer cannot write, an editor
   cannot manage members, another team's admin gets 403.

### Add teams and invitations

Let people work together in workspaces: create one, invite others by
email, give them roles, switch between workspaces. The workspace routes,
the invitation mail and the membership checks are the auth pack's
(`auth.Teams`, on `auth.Roles`); the app adds its pages and scopes its
data to the workspace.

1. Define the roles, with the permissions the workspace routes ask for
   (`members.read`, `members.invite`, `members.manage`,
   `workspace.update`, `workspace.delete`) next to the app's own:

   ```go
   var roles = auth.Roles{
   	"owner":  {"*"},
   	"admin":  {"members.*", "workspace.update", "projects.*"},
   	"member": {"members.read", "projects.*"},
   }
   var teams = auth.Teams{Roles: roles, Title: "notes"}
   ```

   The creator of a workspace gets `owner` (`Teams.Owner` to name
   another). Nobody grants a role with permissions they do not hold,
   and the last owner cannot leave, be removed or demoted.
2. In `app/routes.go`, after `auth.Mount`: `teams.Mount(r)`. Run `lidza
   gen` (the `AuthWorkspace` and `AuthInvite` models), then `lidza db
   migrate`. The client gets `api.workspacesList`, `api.workspaceCreate`,
   `api.workspaceSwitch`, `api.workspaceMembers`, `api.workspaceInvite`,
   `api.workspaceSetRole`, `api.workspaceRemoveMember`,
   `api.invitePreview` and `api.inviteAccept`.
3. An invitation is an email with a one-time link to the app's
   `/invite?token=...` page (`Teams.InvitePath`), valid 7 days
   (`InviteTTL`), bound to the invited address. The page, signed in
   (send the visitor to sign-in or sign-up first, back to the same
   link), shows `api.invitePreview({ token })` (the workspace's name and
   the role) and accepts with `api.inviteAccept({ token })`; a different
   address is a 403 `invite_address`, a used, revoked or expired link a
   410 `invite_gone`. Never put the token in the page's path: the
   request log records paths. `mail/auth_invite.txt.tmpl` (and `.html`)
   replaces the plain text (`.App`, `.Link`, `.Workspace`, `.Inviter`,
   `.Email`, `.Role`).
4. Scope the app's rows to the workspace: a model with `workspaceId uuid
   @index` gets, from `lidza gen resource`, queries that all take it and
   routes behind `auth.RequireWorkspace()`, which reads the workspace
   from the `X-Workspace` header or the cookie `api.workspaceSwitch`
   sets, answers 400 when none is chosen and 404 for one the user is not
   in. A handler of its own reads `auth.WorkspaceID(ctx)` behind the same
   middleware, and checks finer permissions with `roles.Check(ctx,
   auth.WorkspaceID(ctx), "projects.delete")`.
5. Deleting a workspace removes its memberships and invitations;
   `Teams.OnDelete(ctx, tx, id)` deletes the app's own rows in the same
   transaction. `Teams.OnEvent` reports each change (created, invited,
   joined, role changed, removed, left, deleted) for an audit log
   (`audit.Record`). The admin pages list the workspaces.
6. Test it with two accounts: invite, accept, a stranger's 404, a
   member's 403 on an admin route. `lidza test`, then `lidza test --e2e`.

### Add payments

Sell subscriptions: plans in code, Stripe Checkout to subscribe, Stripe's
portal to manage, and the app checking what a plan includes.

1. `lidza pack add billing` (MCP: `lidza_pack_add`; after `db`), then
   `lidza gen` and `lidza db migrate` (`billing_customer`,
   `billing_subscription`). The keys go in the credentials: `lidza
   settings set STRIPE_SECRET_KEY=sk_test_...
   STRIPE_WEBHOOK_SECRET=whsec_...` (test mode in development, the live
   keys in `production.`). `APP_URL` is where Stripe sends customers
   back.
2. Define the plans once, in the app package; each paid plan names its
   Stripe price, and `billing.FreePlan` ("free") what an owner without a
   subscription gets:

   ```go
   var plans = billing.Plans{
   	billing.FreePlan: {Name: "Free", Features: []string{"projects:3"}},
   	"pro":            {Name: "Pro", Price: "price_...", Features: []string{"projects:50", "exports"}},
   }
   ```

3. In `app/routes.go`: `plans.Mount(r, billing.Options{})`. It serves
   `GET /api/v1/billing` (the plan, its status and renewal, the
   features, the plans to choose from), `POST /api/v1/billing/checkout
   {plan}` and `POST /api/v1/billing/portal` (each returns a Stripe URL
   to send the browser to), and Stripe's `POST /api/v1/billing/webhook`.
   Who is billed is the request's workspace (`auth.WorkspaceID`), else
   the signed-in user; `Options{Guard: auth.RequireWorkspace()}` or a
   permission (`roles.Require("billing.manage", ...)`) to restrict who
   may pay. The client gets `api.billingAccount`, `api.billingCheckout`
   and `api.billingPortal`.
4. In Stripe's dashboard: a webhook endpoint at
   `https://<app>/api/v1/billing/webhook` for
   `checkout.session.completed` and `customer.subscription.created`,
   `.updated` and `.deleted`; its signing secret is
   `STRIPE_WEBHOOK_SECRET`. Locally, `stripe listen --forward-to
   localhost:3000/api/v1/billing/webhook` prints a secret for
   development. Events update `billing_subscription`; an older event
   never overwrites a newer one.
5. Check in handlers, never by calling Stripe: `ok, err :=
   plans.Has(ctx, owner, "exports")`, or `plans.Require(ctx, owner,
   "exports")` (a 402 with code `upgrade` the page turns into an upgrade
   prompt). Active, trialing and past-due subscriptions grant their plan;
   canceled and unpaid ones fall back to the free plan.
6. Test with a fake Stripe (`STRIPE_API_URL` points the pack at an
   `httptest` server) and events signed with
   `webhook.StripeSignature(secret, time, body)`: checkout, the
   completed event, an upgrade, a cancellation, a forged event's 401.

### Sell a feature to teams

A feature a workspace pays for and all its members use: teams, billing
and a model together. The pieces are the recipes "Add teams and
invitations", "Add payments" and "Add an LLM feature"; this is how they
meet.

1. Packs: `db`, `auth`, `mail` (invitations), `billing`, `llm`, then
   `lidza gen` and `lidza db migrate`. Workspaces come from `auth.Teams`
   with a role that may use the feature:

   ```go
   var teams = &auth.Teams{Roles: auth.Roles{"owner": {"*"}, "member": {"summaries.create"}}}
   var plans = billing.Plans{
   	billing.FreePlan: {Name: "Free"},
   	"team":           {Name: "Team", Price: "price_...", Features: []string{"ai"}},
   }
   ```

2. Routes in `app/routes.go`: the workspace routes, billing guarded by
   the workspace (so the workspace, not the user, is the customer), and
   the feature in a group that needs a workspace:

   ```go
   teams.Mount(r)
   plans.Mount(r, billing.Options{Guard: auth.RequireWorkspace()})
   ai := r.Group("/api/v1/summaries", auth.RequireWorkspace())
   router.Route(ai, "POST /api/v1/summaries", summarize)
   ```

3. The handler checks the plan first, then calls the model, labelled
   so the admin pages' usage shows what the feature costs:

   ```go
   func summarize(ctx context.Context, req *router.Request[schema.SummaryInput]) (schema.Summary, error) {
   	if err := plans.Require(ctx, auth.WorkspaceID(ctx), "ai"); err != nil {
   		return schema.Summary{}, err // 402 "upgrade": the page offers checkout
   	}
   	res, err := llm.From(ctx).Chat(ctx, llm.Request{
   		System:   "Summarize in one sentence.",
   		Messages: []llm.Message{{Role: llm.User, Content: req.Body.Text}},
   		Label:    "summary",
   	})
   	return schema.Summary{Text: res.Text}, err
   }
   ```

   A long call goes to a job instead (`jobs.From(ctx).Enqueue`, with
   the workspace id in the payload) and the page listens with `useLive`.
4. The page sends the workspace with every call (`X-Workspace`, or the
   current workspace the switch route stores), shows `api.billingAccount`
   to owners, and on a 402 with code `upgrade` offers
   `api.billingCheckout({plan: "team"})`.
5. Test the whole flow with the fake model and a fake Stripe: an owner
   creates a workspace and invites a member (the token from the outbox
   mail), the member gets 402, a signed `checkout.session.completed`
   for the workspace makes it 200, another workspace still gets 402 and
   a stranger sending Acme's id gets 403. The framework's own version of
   this test is `packs/billing/teamflow_test.go`.

### Record an audit event

Keep a durable record of who did what, for operators and compliance:
`lidza pack add audit` (it needs the db pack; the actor comes from the
auth pack).

1. Record after the action, with the outcome:

   ```go
   err := audit.From(ctx).Record(ctx, audit.Event{
   	Action: "post.publish", Resource: "post/" + id, Scope: team,
   	Meta: map[string]string{"version": v},
   })
   ```

   The actor is the signed-in user (`auth.CurrentUser`), never a value
   from the request; a job names itself with `audit.System(ctx,
   "nightly-export")`. `Outcome` is `audit.OK` (the default),
   `audit.Denied` or `audit.Failed`: record refusals too. `Record`
   returns the database's error: an action that must be audited fails
   with it. `RecordTx(ctx, tx, e)` writes inside the change's
   transaction, so both commit or neither does.
2. Metadata is up to 20 short strings the app chose. A key named like
   a secret (`password`, `token`, `key`, `secret`, `cookie`...) or a
   value shaped like one (an API key, a token, a database URL with its
   password) is stored as `[redacted]`. Never pass a request body or
   an environment value.
3. Read it a page at a time, newest first:

   ```go
   page, err := audit.From(ctx).List(ctx, audit.Query{Scope: team, Limit: 50, Cursor: cursor})
   // page.Records, then page.Next as the next Cursor ("" at the end)
   ```

   Filter by `Actor`, `Action`, `Resource`, `Outcome`, `Since` and
   `Until`. Serve it behind a permission (`roles.Require("audit.read",
   team)`).
4. `AUDIT_RETENTION` (a year by default) prunes older records at start
   and daily; `0` keeps them. With the admin pages mounted, every admin
   action is recorded too (`admin.form`, `admin.download`).
5. `lidza gen` and `lidza db migrate` create `audit_event`.

### Add the admin pages

Give operators a place to watch the packs and set their providers,
without writing a page.

1. `lidza pack add auth` if the app has no accounts yet; the pages are
   behind `auth.Require()`.
2. In `app/routes.go`: `admin.Mount(r, admin.Options{Title: "notes"})`
   (import `github.com/agim/lidza/packs/admin`). `Version` (the app's
   own version, such as a constant bumped with each release) shows it
   beside Līdza's in the footer and on the Overview. Once `admin/` holds a
   file (the theme of step 5, a page template), embed it so the binary
   carries it and needs no `admin/` folder beside it: `//go:embed admin`
   above `var adminFiles embed.FS`, and `Templates:
   lidza.Sub(adminFiles, "admin")` in the options.
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
   neutrals); match the app's own tokens instead of a stock blue. The
   file is read from `Options.Templates` (embedded, step 2), else from
   `admin/` on disk.
6. Test it: an admin gets 200 on `/admin/`, another user 403, a visitor
   401; the reference app's `tests/routes_test.go` shows it.
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
   `assets/icons.txt`, a `Group` (the sidebar section, "Billing": pages
   with the same Group are listed together, the groups in the order
   their first page comes; none is "App"; with many pages set
   `admin.Options.FoldGroups` so only the current page's group is
   open), `Template`, `Data` (sqlc queries; the page is
   admin-only, so a query may cross accounts) and `Actions` (a form
   posts to `<Path>/<action>`; return the message the page shows, or an
   error). A form inside a detail view (`?order=1001`) adds
   `<input type="hidden" name="back" value="{{$.Back}}">` so
   its action returns to that view. A page that takes files sets
   `MaxUpload` (bytes) and its form `enctype="multipart/form-data"`; the
   action reads `r.MultipartForm.File`. A file to download (an export, an
   attachment) is a `Downloads` handler at `<Path>/<name>`, behind the
   same gate; it sets `Content-Disposition` and
   `X-Content-Type-Options: nosniff`. A staff audit log is
   `admin.Options.OnAudit(ctx, admin.Audit{Path, Form, Message, Error,
   Download, Status, Request})`: it runs after every form on every page
   (the pack's own and the app's) and every download, with secret
   values already redacted; insert a row with the admin
   (`auth.CurrentUser(ctx)`), and the action needs no audit code of its
   own.
3. The template is `admin/<page>.html` defining `content` (snippet
   `admin-page-template`): Tabler's classes (`card`, `table card-table`,
   `badge`, `btn`), the functions `icon`, `since`, `num`, `bytes`,
   `dict`, and `{{template "admin-empty" (dict "Icon" "inbox"
   "Title" "..." "Text" "...")}}` for an empty list. Embed it so it
   ships in the binary: `//go:embed admin` in `app/routes.go` and
   `Templates: lidza.Sub(adminFiles, "admin")` (the folder, so the
   theme ships too). No inline `<script>` or
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
   and the command, and lists it in `AGENTS.md`. Restart `lidza mcp` to see the new prompt.

### Write a test

Cover a handler with a Go test that boots the app, or a page with a
browser test.

1. Handler: in `tests/<name>_test.go` (`package tests`, importing `notes/app`) or a unit test beside its non-root package:

   ```go
   func TestCreateThing(t *testing.T) {
       srv := lidzatest.Start(t, app.New(nil))
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
   test database and runs `go test ./...`.
3. Page: in `e2e/<name>.spec.ts` (Playwright) load the page, assert on
   text or roles, and assert no `window` errors (see `e2e/home.spec.ts`).
   Run `lidza test --e2e` (add `--install` once if the browser is
   missing).

### Organize application packages

Place app-owned Go code consistently as the app grows; preserve generated
contracts and keep the same instructions available to every agent.

1. Read the brief, decisions and layout in this guide. With `appDir`, keep
   the application factory, routes, startup, tools and embedded assets there;
   `main.go` stays the entrypoint. Keep HTTP and job handlers in `handlers/`,
   where `lidza gen resource` writes them. Integration tests live in `tests/` and use the importable factory; unit tests
   stay beside their non-root package. Root tests and stray files fail L020.
   Keep docs in `docs/`, scripts in `scripts/`, fixtures in `testdata/`, and
   scratch/build output in `.lidza/` or `bin/`.
2. Put business behavior in `internal/<feature>/` (for example
   `internal/orders/`), vendor clients in `internal/providers/<vendor>/` and
   shared infrastructure in `internal/platform/<name>/`. Create packages as
   features need them. Go enforces the `internal` import boundary. An app that
   needs a reusable public library should extract a separate module.
3. Declare models, API shapes and enums in `schema.lidza`. Write SQL in
   `db/queries/*.sql`. Keep generated `schema/`, `db/queries/gen/`, pack wrappers
   and `.lidza/` at their existing paths. Handwritten behavior belongs with its
   feature; do not introduce a second models folder or edit generated structs.
4. Wire services in the startup hook and pass dependencies through the existing
   services registry or constructors. Feature and provider packages do not
   import the application factory or its handlers. Avoid import cycles and
   keep the scalability rules, deadlines and test stubs.
5. For existing code, move files with `git mv`, preserve tests and assertions,
   update imports and current documentation references, and carry embedded
   assets and package-relative fixtures with their package. Keep historical
   migration source references unchanged. Run `lidza gen` afterwards.
6. Shared rules, agreements and notes go in `AGENTS.md`, the one
   instructions file (`CLAUDE.md` and `GEMINI.md` import it). Edit recipes
   in this guide; `lidza gen` updates both agents' skills and Gemini
   commands.
7. Record the layout decision in `docs/decisions.md`. Verify discovery with
   `lidza api --list` and `lidza api ./internal/orders --filter Name` for a
   real package and declaration. App internal packages are discoverable through
   CLI and MCP; framework internals are not an app API.
8. Run `lidza check`, `lidza test` and `lidza verify`. Run the browser suite
   for an application move that changes its wiring or embedded assets.

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
