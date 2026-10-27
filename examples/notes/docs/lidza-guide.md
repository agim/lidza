# Līdza guide for notes

Written by `lidza new`. One source of truth for every agent working here;
`AGENTS.md` points at this file (`CLAUDE.md` and `GEMINI.md` are the
line `@AGENTS.md`, which Claude Code and Gemini CLI expand; Codex reads
`AGENTS.md` itself).

## What this is

A Līdza app is one Go binary. In production it serves the API under `/api`
and the built frontend for every other path: a file the build lacks
(`/robots.txt`, a stale hashed asset) is a 404, and any other path is a
client-side route. In development, `lidza dev`
runs the same binary with the frontend proxied from its dev server, so the
app is always at one address: http://127.0.0.1:3000.

## Layout

```
notes/
├── main.go          entrypoint: lidza.Run(...). Do not edit.
├── app/            importable application factory and wiring
├── tests/          integration tests importing app.New
├── app/start.go         onStart: register job handlers, provide services; appMiddleware: middleware around the whole app (rate limits, redirects). The app's hooks.
├── app/routes.go        API handlers. Add routes here (or in packages it calls).
├── handlers/        HTTP and job handlers; resource generation writes here
├── internal/        app-owned business logic, provider clients and infrastructure
├── schema.lidza     data shapes: models (tables), types (API shapes), enums
├── schema/          generated Go structs with Validate(). Do not edit.
├── db/              generated schema.sql, migrations/, schema.lock.json; queries/*.sql with the db pack
├── app/packs.go         generated from lidza.json "packs". Do not edit.
├── app/tools.go         the app's MCP tools (lidza.ToolFunc), served by lidza mcp and /mcp
├── packs/<name>/    a pack: pack.lidza.json, rust/ crate, generated pack.go and <name>.wasm
├── lidza.json       project config: name, frontend template, dev server, dist, appDir
├── go.mod           module notes, requires github.com/agim/lidza
├── package.json     frontend (react); npm scripts dev, build, check
├── src/             frontend source
│   ├── router.tsx   client-side routes
│   └── pages/       one file per page; API calls via `import { api } from '@lidza/client'`
├── .lidza/client/   generated TypeScript client (gitignored, `lidza gen`)
├── dist/            frontend build output, embedded into the binary. Never edit.
├── .githooks/pre-commit  runs lidza verify before every commit
├── .claude/skills/  one skill per recipe below (Claude Code), generated from this file
├── .agents/skills/  the same skills for Codex and Gemini CLI
├── docs/lidza-guide.md   this file
├── docs/decisions.md     why the app is built a way: packs, Rust, dependencies, tradeoffs
└── .lidza/          dev build artifacts (gitignored)
```

### The app in its own package (`appDir`)

New apps set `"appDir": "app"`: `app/app.go` exports `New(dist fs.FS)`;
`main.go` keeps the frontend embed and calls `lidza.Run(app.New(...))`.
Wiring (`start.go`, `routes.go`, `packs.go`, `tools.go`) lives in `app/`.
Integration tests in `tests/` import `notes/app` and call `app.New(nil)`;
unit tests stay beside their non-root package. The htmx template embeds
`app/views/` and `app/static/`; its tests also live in `tests/`.

Root files are limited to the entrypoint, schema/configuration, dependency
manifests, deployment/tool configuration and shared guidance. L020 is an
error for root tests, extra Go files and unexpected files, including ignored
or untracked files: keep docs in `docs/`, scripts in `scripts/`, fixtures
in `testdata/`, and scratch/build output in `.lidza/` or `bin/`.
Never hide tests from discovery or weaken assertions to satisfy the layout.

An older app with root wiring needs an importable package because Go cannot
import package `main`. Preserve its tests and behavior during the move:

1. Move `start.go`, `routes.go`, `tools.go` and the other files of the
   root package except `main.go` into `app/`, with `package app`. The
   function that builds the `lidza.App` becomes `func New(dist fs.FS)
   lidza.App` in `app/app.go`.
2. `main.go` keeps the embed and runs it:
   `func main() { lidza.Run(app.New(lidza.Sub(dist, "dist"))) }`.
   A build served from disk instead of embedded goes through
   `lidza.DirFS(dir)`, never `os.DirFS`: it refuses a symlink or path
   that leads out of the directory.
3. Add `"appDir": "app"` to `lidza.json` and run `lidza gen`: `packs.go`
   is generated into `app/` (delete the root one). `lidza gen resource`
   registers routes in `app/routes.go` and `lidza mcp` reads
   `app/tools.go`.
4. A directory a moved file embeds moves with it (`//go:embed admin` in
   `routes.go`: `app/admin/`; the i18n pack's catalogs: `app/locales/`).
5. Tests go in `tests/` (`package tests`) and start the app with
   `srv := lidzatest.Start(t, app.New(nil))`; `lidza test` runs them.

## Commands

| Command | What it does |
|---|---|
| `lidza dev` | starts the frontend dev server (http://127.0.0.1:5173), builds and runs the app on http://127.0.0.1:3000, rebuilds on Go changes. `npm install` runs automatically when `node_modules` is missing. |
| `lidza build` | builds the frontend into `dist/` (routes without parameters prerendered to static HTML), then compiles `bin/notes`: one binary, no Node at runtime. |
| `lidza check --json` | regenerates, then runs `go vet`, `staticcheck`, `cargo check`, `tsc` and `eslint`, the rules, and the dependency scanners when a lock file changed or a day passed since the last scan (`--security` runs them now; `--jobs N` caps the whole-program checkers running at once, half the CPUs by default); prints one JSON list of diagnostics, each with `layer`, `file`, `line`, `message`. Exit 1 while there are errors. A frontend call that no longer matches a handler fails here. |
| `lidza gen` | `schema.lidza` to `schema/schema.go`, `db/schema.sql`, a migration in `db/migrations` when models changed; handlers to `.lidza/openapi.json` and the client in `.lidza/client`. `lidza dev` runs it on every change. |
| `lidza context` | writes `.lidza/context.json`: routes, handler signatures, Rust exports. |
| `lidza mcp` | MCP server on stdio; see "Agent interface". |
| `lidza gen resource <Model> [--public \| --shared]` | queries, `Create/Update/<Model>List` types, `handlers/<table>.go` with list, get, create, patch, delete under `/api/v1/<plural>` (the list with search, filters, a date range, sorting and pages through `list.Read`: recipe "Add a paginated, filterable list"), registered in `app/routes.go` (in `appDir` when set) behind `auth.Require()`. An owned model (`ownerId`, or a field with `@ref(User)`) is scoped to the signed-in user. `--public` marks the model `@public`: open routes, no owner. `--shared` marks it `@shared`: behind sign-in, rows not scoped. Needs the `db` and `auth` packs (`auth` not with `--public`). |
| `lidza update [--migrate]` | brings the app's branch and the framework up to date together: a branch behind its upstream is pulled first when that is a clean fast-forward (nothing uncommitted, no local commits the upstream lacks), otherwise the update stops and says what to run (`--no-pull` stops instead of pulling); then the CLI and this app's framework module move to the newest release (never back to an older one; `--to <commit>` takes a release not tagged yet), it regenerates, refreshes the deployment files, then runs `lidza install`. It stops when the files it rewrites have uncommitted changes (`git stash push` sets them aside); `--commit` commits the update when done. In a team, one person makes the update and merges it; the others `git pull`, then `lidza update --cli-only` (`--to <go.mod's version>` when the app is behind the newest). While the CLI and `go.mod` differ, gen, dev and install leave the agent files alone and say so, and `lidza verify` fails. |
| `lidza gen deploy [--force]` | the `Dockerfile`, `.dockerignore` and `deploy/notes.service` from the current templates; a file the app changed is kept and named unless `--force`. |
| `lidza pack add <name>` | enables an official pack: `db`, `auth`, `jobs`, `mail`, `llm`, `storage`, `cache`, `i18n`, `realtime`, `analytics`, `audit`, `hooks`, `billing`, `geo`, `media`. |
| `lidza settings set NAME=value` | saves app settings (keys, providers, addresses) in the app's database, sealed with the master key: `list` (where each comes from, names only), `get NAME`, `unset NAME`, `import` (the app settings in `.env`, `deploy/production.env`, the credentials file and the environment, into the database); `--production` for production's database. Infrastructure settings are refused: they are the environment's. |
| `lidza credentials set NAME=value` | the development seed: seals a value into `config/credentials.yml.enc` with `config/master.key` (`dev.NAME` for one mode); `list` (names), `show` (every value, decrypted) or `show NAME`, `edit` (the whole file in `$EDITOR`), `unset`, `init`. |
| `lidza admin add EMAIL...` | lets these users (emails or ids) open `/admin` besides the first account: `ADMIN_USERS` in the sealed credentials, read within seconds; in production after a commit and deploy of `config/credentials.yml.enc`. Admins added on the Users page, in the file and in the environment all count. `remove`, `list`. |
| `lidza admin owner status\|rotate [--production]` | with `AUTH_OWNER_CLAIM=true`: whether the app waits for its owner and where the one-time token file is (never the token); `rotate` replaces an unclaimed token at once on every node. |
| `lidza brief [--all] [--list]` | the kickoff interview in a terminal: the open questions of `docs/brief.md` one by one, each with suggestions to pick by number or your own answer; `--all` goes through every question, `--list` prints them. `lidza brief answer <id> "..."` records one; `lidza brief skip [id...]` skips questions for good (no ids: every open one), and `s` or `S` in the interview skips one or the rest. |
| `lidza note add "..."` | a lasting fact about this app in `docs/notes.md`, shared through git; `lidza note list [--grep word] [--since date]` finds the ones for an area. |
| `lidza test [-v] [--run Regexp] [-p N] [--fresh] [go test flags]` | creates and migrates the `.env.test` database (`--fresh` drops and recreates it first, after a migration from another branch), runs `go test ./...` with `LIDZA_MODE=test` (each test package on its own copy of that database, `-p` packages at once). The frontend type-check and lint are `lidza check`'s. Tests never see the master key or a variable named like one of the app's credentials: they run on `.env.test` and the test providers; a test that needs a key sets `LIDZA_MASTER_KEY` itself. |
| `lidza test --e2e [--install]` | builds the app, starts the binary with `.env.test`, runs the Playwright suite in `e2e/`; `--install` fetches the browser when missing. The sign-in and analytics rate limits are raised for the run (every account signs up from 127.0.0.1) unless `.env.test` sets them; the Go tests keep the real limits. Rows the suite needs that no page creates (data a sync job would fetch) go in `e2e/seed.sql`, loaded into the test database before each run; write it to be rerun (`ON CONFLICT DO NOTHING`). |
| `lidza install [--packs db,auth,mail] [--agent claude]` | on an existing app: enables the packs (db added when one needs it), writes `.env` from `.env.example` with a random `AUTH_SECRET` and the app's database, writes `.env.test`, generates, creates and migrates the dev and test databases, installs `node_modules`, installs the agent CLI when asked, makes the first commit. On a clone or after a pull it adds to `.env` what packs enabled since need and applies new migrations; one that drops data (`-- review: data loss`) waits for `--migrate`. `lidza new --packs ...` runs it for a new app; `lidza setup` is its former name. Rerunning is safe. |
| `lidza ship [--domains a.example.com] [--email ops@example.com] [--no-e2e]` | before a deploy: `lidza verify`, the browser suite against the built binary, the production build, `deploy/production.env`; stops at the first failure. Then it names the settings the enabled packs need in production that neither the credentials nor `deploy.env` hold, or hold with a development-only value (`CACHE_URL`, `APP_URL`, `LLM_PROVIDER=fake`). The app's production settings that are not secrets (`MAIL_PROVIDER`, `STORAGE_BUCKET`, `STORAGE_PREFIX`) go in `lidza.json` under `deploy.env`, which ship writes into `deploy/production.env` on every run; a name that looks like a secret there is refused. |
| `lidza verify [--json] [--no-test] [--allow-test-changes]` | before a commit: regenerates and requires the generated files to be staged, refuses staged changes that weaken the tests, runs the checks, runs the Go tests. The pre-commit hook in `.githooks/` runs it (`git commit --no-verify` skips once; `lidza verify --install-hook` sets it up again). The test guard compares the staged changes with `HEAD` and names each file and line that deletes a test file (`*_test.go`, `*.spec.ts`, `*.test.ts(x)`), adds `t.Skip`, `t.Skipf`, `t.SkipNow`, `test.skip`, `it.skip`, `describe.skip`, `.only` or `.fixme`, or removes more assertions (`t.Error`, `t.Fatal`, `expect(`, `assert.`) from a file than it adds, or swaps an assertion's matcher for a looser one (`toBe` to `toBeTruthy`, `assert.Equal` to `assert.NotNil`). An assertion whose expected text changes and nothing else (a copy update: `'Security'` to `'Sign-in security'`) passes, and the verify report lists each one, old and new, for the developer to see. A deliberate change is confirmed with `LIDZA_ALLOW_TEST_CHANGES=1 git commit ...` (or `lidza verify --allow-test-changes`); one skip with a stated reason passes with a `// lidza:allow-skip <reason>` comment on its line or the line above. A test is never weakened to make it pass: if it is wrong, the agent says so and the developer confirms.. A check or a test run that already passed on this very tree (`.lidza/checked.json`, `tested.json`) is not repeated, and the pre-commit hook (`lidza verify --hook`) does not repeat a verify that passed on the index being committed; `--strict` repeats everything and scans the dependencies |
| `lidza gen llms [--force]` | writes a starting `llms.txt` (llmstxt.org) for visiting agents: the app's name as the H1, a summary to write, a link per page the last build prerendered by its title. It goes in `public/` (`app/static/` in the htmx template) and is served at `/llms.txt` as text. It is public content: what an agent needs to use the site, never the guides, handlers, routes or configuration. Without it `/llms.txt` is a 404. |
| `lidza audit layout [--viewport 1440x900,390x844] [--theme light,dark] [--routes /a,/b] [--max-scroll -1] [--stability 6s [--trigger JS] [--allow SELECTORS]] [--base-url URL] [--storage-state FILE \| --login FILE] [--json]` | builds and starts the app on the test database (as `lidza test --e2e`), visits every page the build prerenders plus `--routes`, signed in as a throwaway user when the auth pack runs, at each viewport and theme, and reports what scrolls: sideways is a fault (the elements past the edge are named), down is a fault past `--max-scroll` pixels (`-1`, the default, only reports it; an app whose design says no page scrolls sets `0`). Containers that scroll on their own (a wide table in its wrapper) are listed with their sizes, never a fault. `--stability 6s`, on every page, scrolls the document and those containers part way, focuses a control without scrolling to it, runs `--trigger` (a refresh call) once or waits, and faults a container a re-render reset, a document whose reading anchor (the element at the reader's eye line) moved or vanished, and lost focus; an update that keeps the anchor in place passes even when the offset changes. A log that follows its tail on purpose is named in `--allow` or carries `data-audit-follow` (on `body` for the document). `--base-url` audits an app already running (any Go app with Playwright in `node_modules`), signed in with `--storage-state` (a Playwright storage state) or `--login` (a module whose default export, `async (page, baseURL)`, signs in). A hash route is a same-document navigation, without an HTTP status. Exits non-zero on a fault. The same checks for specs: `expectNoSidewaysScroll(page)` and `expectFitsViewport(page)` from `e2e/layout.ts`. |
| `lidza audit performance [--samples 3] [--budget lcp=2500,js=300kb] [--routes /a] [--base-url URL] [--json]` | builds and starts the app and loads every prerendered page plus `--routes` cold on a throttled phone (Lighthouse's mobile settings), `--samples` times, reporting the median FCP, LCP, CLS and TBT, the bytes downloaded by kind and the JavaScript that does not run on load. A page over a budget is a fault: sizes and layout shift by default (`cls=0.1`, `js=300kb`, `css=100kb`, `images=1000kb`, `total=1600kb`), timings only when set (`--budget fcp=1800,lcp=2500,tbt=200`, in ms), since they vary between machines; the advice is not: uncompressed text, assets cached briefly, images larger than drawn ("Add a responsive image") or without a size, a lazy LCP image, a missing title, `lang`, viewport or description, and an `llms.txt` served as HTML. Gate on timings with more `--samples`, and set budgets for the app's own pages. A hydrating page's TBT falls to zero as a static page ("Add a page"). |
| `lidza doctor` | toolchain, services, `node_modules`, pack builds, e2e browser; each missing item with its fix. |
| `lidza pack scaffold <name>` | creates `packs/<name>` with a crate and an example capability; `lidza pack build` compiles it. |
| `lidza db migrate\|rollback\|status [--production]`, `lidza db new "<description>"` | applies `db/migrations` (db pack, `DATABASE_URL` from `.env`); `new` writes a hand-written (data) migration that `lidza gen` never touches. `migrate` and `rollback` refuse a database on another host (not a Unix socket, `localhost` or a loopback address) unless `--production` is given; the MCP tools never pass it. |
| `lidza benchmark [--vus 500] [--duration 1m]` | runs `benchmarks/scale_test.js` with k6 against the running app; heap and goroutines must stay flat. |
| `lidza version` | prints the framework version. |
| `go test ./...` | Go tests. |
| `npm run check` | frontend type check and lint (`tsc --noEmit && eslint src`; `jsx-a11y` violations are errors). |


## Agent interface

`.mcp.json` (Claude Code), `.gemini/settings.json` (Gemini CLI) and
`.codex/config.toml` (Codex, once the project is trusted) start
`lidza mcp` for this project; `lidza gen` writes one an app lacks. Every command of the CLI is a tool, so an
agent needs no shell here. Its tools:

| Tool | Returns |
|---|---|
| `lidza_routes` | the API routes with handler name, file, line and signature |
| `lidza_context` | the whole project as JSON (same as `.lidza/context.json`) |
| `lidza_check` | the diagnostics of `lidza check --json` |
| `lidza_logs` | the last lines of the `lidza dev` output (`lines`, `filter`) |
| `lidza_config` | `lidza.json` |
| `lidza_gen`, `lidza_gen_resource` (`model`, `force`), `lidza_pack_add` (`name`), `lidza_pack_scaffold`, `lidza_pack_build`, `lidza_db_migrate`, `lidza_db_rollback`, `lidza_db_status`, `lidza_test` (`e2e`, `install`, `run`, `verbose`), `lidza_verify` (`no_test`), `lidza_build`, `lidza_ship` (`domains`, `email`, `no_e2e`), `lidza_doctor` | the CLI command of the same name run for you, in this project: one JSON result with `ok`, `exit`, the parsed `report` (check, verify) or the `output`. Prefer these over a shell. |
| `lidza_recipes`, `lidza_recipe_add` | the recipes with their scope; record one of this app's conventions (title, description, steps) |
| `lidza_decisions` (`grep`), `lidza_decision_add` | the decisions behind an area, by a word in them; record why the app is built a way (a pack, Rust, a dependency, a schema tradeoff) in `docs/decisions.md` |
| `lidza_notes` (`grep`, `since`) | the team notes for an area, from `docs/notes.md` |
| `lidza_brief` (`open_only`), `lidza_brief_answer` (`id`, `answer`), `lidza_brief_skip` (`ids`), `lidza_note_add` (`text`) | the brief: its questions with suggestions and answers, one answer recorded and applied (a decision, the working agreements, the palette, a seeded recipe); a lasting fact in the Team notes. `lidza://brief` reads `docs/brief.md` |
| `lidza_credentials_set`, `lidza_credentials_list` | seal secrets into `config/credentials.yml.enc` by their environment names; list the names |
| `lidza_mail`, `lidza_llm`, `lidza_llm_usage`, `lidza_storage`, `lidza_errors` | with the pack enabled: the outbox; a prompt against the configured model; token usage; stored objects; captured errors |
| `lidza_api` | the framework's public Go API: one `package`, or a `filter` across all; without either, the package list; also the resource `lidza://api/{package}` |
| `lidza_snippet` | a file of the reference app, verified by its tests (`name`: `schema`, `routes`, `auth-handlers`, `resource-handlers`, `queries`, `handler-test`, `mcp-tool`, `page`, `browser-test`); also `lidza snippet` |
| `lidza_errors` | captured errors (analytics pack) |

Its prompts are the recipes of this guide (`add-api-route`,
`add-resource`, ...). The same recipes are skills for Claude Code
(`.claude/skills/`), and for Codex and Gemini CLI (`.agents/skills/`,
invoked as `$add-api-route` in Codex and `/add-api-route` in Gemini);
`lidza gen` rewrites them from this file.

The app adds its own tools in `app/tools.go` with
`lidza.ToolFunc("name", "what it does", func(ctx, In) (Out, error))`;
they appear as `app_name` in `lidza mcp` (running inside the app, with
its packs) and, when the binary runs with `LIDZA_MCP_TOKEN`, at `/mcp`
for other agents. Give a tool a `schema.lidza` type as `In` and its rules
are enforced.

While `lidza dev` runs, http://127.0.0.1:3000/_lidza/llms.txt summarizes
the app and http://127.0.0.1:3000/_lidza/llms-full.txt has this guide plus
every handler signature. Both are regenerated after each Go rebuild. They
are also at /llms.txt and /llms-full.txt until the app serves its own
there (a product's llms.txt for crawlers), which then wins.

The lists are live: a pack added to `lidza.json`, a recipe recorded in
the guide or a tool added to `app/tools.go` appears in this server's tools
and prompts within seconds, and at once after `lidza_pack_add` and
`lidza_recipe_add`; no restart. Only a newer CLI binary (`lidza update`)
needs the server reconnected (`/mcp` in Claude Code), and every command
result says so while that is the case.

## Rules

1. Go owns `/api`. The frontend never defines an API route and never proxies
   one; it calls `/api/...` on the same origin.
2. Add an API route in `app/routes.go` with `router.Route(r, "GET /api/v1/things/{id}", handler)`
   where `handler` is `func(ctx context.Context, req *router.Request[In]) (Out, error)`.
   `In` is the JSON body type (`router.None` without one) and `Out` the reply
   (`router.None` for 204). Put the shapes in `schema.lidza` so they get
   validation and reach the client. Return `router.NotFound("thing")` or
   `router.Errorf(status, ...)` for client-visible errors, or
   `router.ErrorCode(status, "code", ...)` when a client must tell two
   errors of one status apart (it reads `err.code`); any other error is a
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
   <package> [--filter name]` or the MCP tool `lidza_api` with one
   package (`packs/llm`), or a filter alone to search every package by
   name; without either, the package list. Never render them all
   (`all`): thousands of lines every later turn carries. `lidza api app`
   (or `./handlers`) does the same for this app's own packages. An import
   of a framework package that does not exist fails `lidza check` (L004).
10. A pattern this app uses twice is a recipe: write it under "App
   recipes" (`lidza recipe add "Title"`) so the next task follows it.
11. Before writing a handler, page, test or tool of a kind you have not
   written here, read the matching snippet (`lidza snippet` or the MCP
   tool `lidza_snippet`): it is the reference app's code, verified by its
   tests.
12. The brief (`docs/brief.md`), the working agreements in `AGENTS.md`
   and the team notes in `docs/notes.md` are this app's shared memory:
   `lidza note list --grep word` (MCP `lidza_notes`) finds the notes for
   the area you work in; fill the
   brief with the developer while it has open questions (recipe "Start
   with the brief"), and record a lasting fact with `lidza note add` (MCP
   `lidza_note_add`), never in an agent's local memory. `lidza check`
   L015 warns while a required question is open.
13. A pack (Rust or official), a dependency, an integration or a schema
   tradeoff is a decision: `lidza decision list --grep <area>` (MCP
   `lidza_decisions`) before changing an area, and record yours in the same commit (`lidza decision add
   "Title" --why "..."`, MCP `lidza_decision_add`; `lidza pack add --why`
   records it for a pack). `lidza check` L012 flags a pack or a direct
   dependency no decision names, and L011 a pack no code uses; CI runs
   `lidza verify --strict`, where warnings fail.

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
| Translated pages | `src/i18n.ts`, a page per locale at build | same | none | Go handlers (`i18n.From(ctx).T`) |
| Analytics reporter | `VITE_ANALYTICS=1` | `VITE_ANALYTICS=1` | `PUBLIC_ANALYTICS=1` | `ANALYTICS_FRONTEND=1`, `static/analytics.js` |
| Styling | Tailwind v4 | `app.css` | `<style is:global>` in the layout | `static/app.css` |

This app uses the **react** template.

## Parallel work

Several agents or branches at once, each in its own git worktree
(`git worktree add ../app-feature feature`), run `lidza install` there
first. It copies what git does not carry from the main checkout (`.env`,
`config/master.key`), links `node_modules` when `package-lock.json` is the
same, and gives the worktree databases of its own (`<app>_feature_dev`,
`<app>_feature_test`) in `.env.local` and `.env.test.local`, the
checkout's own settings files that win over the committed ones, so one
worktree's migrations never land in another's database. `lidza test`
does the test database part by itself. `LIDZA_DB_SUFFIX` names any
checkout's databases apart.

Merging the branches back: migrations are named by time, so two never
share a name; `docs/decisions.md` and `CHANGELOG.md` keep both branches'
entries (git's union merge, in `.gitattributes`); `db/schema.lock.json`
merges model by model through the driver `lidza gen` registers in the
clone (`merge.lidza-lock`). Two branches that changed the same model
differently stop the merge on the lock: merge `schema.lidza`, then
`lidza gen`. After any merge, `lidza gen` (or `lidza dev`) regenerates
the rest.

## Tests

`tests/routes_test.go` shows the shape: `srv := lidzatest.Start(t, app.New(nil))`
boots the app with its packs (`.env.test`, `LIDZA_MODE=test`) and
`srv.JSON(t, method, path, body, &out, lidzatest.Bearer(token))` calls it.
Run `lidza test`.

Many tests run faster split by area into packages (`tests/billing/`,
`tests/sites/`): `go test` runs packages in parallel, and a change reruns
only the packages it touches. Each package gets its own copy of the test
database (`<app>_pkg_tests_billing_test`, cloned from the migrated
`.env.test` database when its first test starts), so their rows never
collide; `lidza test` drops the copies at the end, and `lidza test -p 4`
caps how many packages run at once. Tests inside one package share their
copy and run one after another (`lidzatest.Start` sets the working
directory and environment, which a `t.Parallel` test cannot).
`LIDZA_TEST_SHARED_DB=1` keeps every package on the one database.

Time and outbound HTTP are testable when handlers use `lidza.Now(ctx)`
instead of `time.Now()` and `lidza.HTTPClient(ctx)` instead of
`http.DefaultClient`: `srv.Clock.Set(t)` freezes time, and
`lidzatest.Start(t, app.New(nil), lidzatest.WithRecorder("name"))` replays
`testdata/http/name.json`, recorded once with `LIDZA_RECORD=1 lidza test`.

Layout in the browser suite: `e2e/layout.ts` has `expectNoSidewaysScroll(page)`
(nothing wider than the viewport, a bug at any width) and
`expectFitsViewport(page)` (the page does not scroll down, for an app
whose design says so); the home spec checks a phone's width, and `lidza
audit layout` checks every page at every viewport and theme.

Call a pack from the test as a handler would with `srv.Context()`:
`mail.From(srv.Context()).WaitFor(ctx, to, subject, 5*time.Second)` waits
for a message a job sends; `jobs.From(srv.Context()).Get` reads a job.
A test acting as several users passes each one's token with
`lidzatest.Bearer(token)`; the server's client keeps the cookies of the
last sign-in. `lidza test -v` (MCP: `lidza_test` with `verbose`) lists
every test as it runs.

Browser tests live in `e2e/*.spec.ts` (Playwright); `lidza test --e2e`
runs them against the built binary. If the browser is missing the command
prints the install line; `lidza test --e2e --install` runs it (`lidza
setup` installs it up front). Locate by role and accessible name, and
pass `exact: true` when one name is a prefix of another ("Comment" and
"Comments", a card and its "Move ... to Done" button).

## Recipes and skills

Each task has a recipe, step by step: the framework's and this app's own are
in `docs/lidza-recipes.md`, and every one is a skill (`.claude/skills/<name>`
for Claude Code, `.agents/skills/<name>` for Codex and Gemini CLI) and a
`lidza mcp` prompt of the same name. Load the one the task needs; the names
are listed in `AGENTS.md`. Record this app's conventions with `lidza recipe
add` (MCP `lidza_recipe_add`); a pattern used twice is a recipe.
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

The official `media` pack (`lidza pack add media`) reads an image's size
and format (`ImageInfo`) and resizes it (`ImageResize`) to jpeg, png or
webp. `Fit` picks how: `contain` (the default) scales it inside
`Width` by `Height`, never enlarged; `cover` fills exactly that size and
crops the overflow around the centre, for square avatars and thumbnails.
It decodes jpeg, png, webp, gif and bmp. HEIC (what phones take), AVIF
and TIFF fail with the code `unsupported_format`: there is no mature
pure-Rust HEIC decoder and the pack takes no C library, so the app keeps
the original file:

```go
out, err := media.From(ctx).ImageResize(ctx, schema.ImageResizeInput{Data: data, Width: &size, Height: &size, Fit: &cover})
if engine.ErrorCode(err) == "unsupported_format" {
	// store the original as it is; no thumbnail
}
```

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
  To let the caller branch on the kind of failure, return
  `Result<Out, abi::Error>` and `Err(abi::Error::code("too_large",
  "..."))`; Go reads it with `engine.ErrorCode(err)`. A `String` error
  converts into one without a code, so `?` works on either.
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
  `auth.From(ctx).Logout(ctx, user.SessionID)`. Delete an account and
  every row the pack keeps with `auth.From(ctx).DeleteUser(ctx, id)`
  (or `DeleteUserTx` in the app's transaction); with `auth.Mount`,
  `OnSignUp` and `OnDeleteUser` run the app's code in the same
  transactions (recipe "Add sign-in"). Sessions slide: when
  a browser's access token has expired, `Require` and `Optional` renew
  it from the refresh cookie on that request and set both cookies again,
  so a tab stays signed in for `AUTH_REFRESH_TTL` after its last use
  (requests racing on an expired token rotate the session once; a
  database error is a 503 that keeps the cookies) without a refresh
  route or client code; a bearer client refreshes through a route of the
  app that calls `auth.From(ctx).Refresh`. Without "remember me":
  `auth.From(ctx).LoginWith(ctx, userID, claims,
  auth.SessionOptions{SessionOnly: true})`; its cookies and every
  renewal's are session cookies, gone when the browser closes. Read what changes after login
  (a verified flag, a role) from the database, not from the token's
  claims, which are what they were at login. Hardening: wrap the
  credential routes with `auth.Throttle()` (per-client rate limit,
  `AUTH_LOGIN_RPS`) and the sign-in route with `auth.ThrottleSignIn()`
  (its own limit, `AUTH_SIGNIN_RPS`, else `AUTH_LOGIN_RPS`), refuse weak
  passwords with
  `auth.From(ctx).ValidatePassword(password, email)` (length, common
  passwords, the email itself), and run email verification and password
  reset on one-time tokens: `IssueToken(ctx, auth.PurposeVerifyEmail,
  email, 0)` makes the token the app mails, `ConsumeToken` redeems it
  once; `RevokeAll` after a reset. Working code: the snippets `routes`
  and `auth-handlers`.
- `jobs`: register handlers in `app/start.go` (`onStart`) with
  `jobs.FromServices(s).Handle("kind", fn)`; enqueue with
  `jobs.From(ctx).Enqueue(ctx, "kind", payload, jobs.RunAt(t))`, or
  `EnqueueTx(ctx, tx, ...)` inside a transaction; recurring work with
  `jobs.FromServices(s).Schedule("kind", jobs.Weekly(time.Monday,
  "09:00", "Europe/Tirane"), payload)` (or `jobs.Every(d)`,
  `jobs.Daily("06:30", zone)`), one job per due time across nodes. The
  handler's context carries the packs (`db.From(ctx)`, `mail.From(ctx)`)
  and is cancelled at shutdown after `JOBS_DRAIN`, the job going back to
  pending. Recipe: "Add a background job".
- `cache`: `cache.Remember(ctx, cache.From(ctx), "key", ttl, load)`;
  `cache.From(ctx).Invalidate(ctx, "prefix:")` after writes. Counters
  every node shares (a rate limit, a quota): `n, err :=
  cache.From(ctx).Incr(ctx, "rate:"+ip+":"+minute, 1, time.Minute)` adds
  atomically (a Lua script on Valkey) and returns the new value; the
  increment that creates the key sets the ttl, later ones keep it.
- `i18n`: `i18n.From(ctx).T(ctx, "key", args...)`, `Number`, `Currency`,
  `Date`, `Time`, `DateTime`, `Relative`, `Today` and `At` (a typed local
  time to its instant) in the visitor's zone (the template sets a `tz`
  cookie, API clients send `X-Timezone`; `I18N_TIMEZONE` is the default;
  recipe "Dates and time zones"); catalogs in `locales/<lang>.json` (nested keys join with
  dots); serve them to other clients with
  `r.Handle("GET /api/v1/i18n/{lang}", i18n.Handler())`. Store and send
  times in UTC; format at the edge. The mail pack writes in the
  request's locale (`mail/<name>.<lang>.txt.tmpl`). Pages (react and
  svelte templates) translate with `t('key', args...)`, `locale()` and
  `setLocale(lang)` from `src/i18n.ts`, over the same catalogs. With more than one catalog
  `npm run build` writes every prerendered page once per locale
  (`dist/.locales/<lang>/`, the `I18N_DEFAULT` one also at the plain
  path), and the binary serves the one the request negotiates (`?lang`,
  the `lang` cookie, `Accept-Language`, then `I18N_DEFAULT`) with
  `Vary: Accept-Language, Cookie`; the page carries `<html lang>` and its
  catalog, so nothing renders in another language first. `LIDZA_SSR=1`
  renders in the same locale; paths that are not prerendered get that
  locale's shell. `setLocale` sets the `lang` cookie and reloads. An app
  created before `src/i18n.ts` existed takes it, `scripts/` and the
  i18n lines of `src/main.*` and `src/entry-server.*` from a new app
  (`lidza new tmp --template react --no-setup`, or `svelte`). Recipe:
  "Add a page".
- `realtime`: `realtime.From(ctx).Publish(ctx, topic, value)` and
  `r.Handle("GET /api/v1/realtime", realtime.Handler(realtime.Authorize(fn)))`
  behind `auth.Require()`; `fn(r, topic)` decides per topic. Without it
  no topic can be subscribed; `realtime.Authorize(realtime.AllowAll)`
  opens every topic to every connection. Recipe: "Publish live
  updates".
- `mail`: `mail.From(ctx).Send(ctx, mail.Message{To, Subject, Template:
  "verify", Data: data})` renders `mail/verify.txt.tmpl` and
  `mail/verify.html.tmpl` (Go templates over `Data`; links through
  `mail.From(ctx).Link(path)`, absolute with `APP_URL`), or
  `mail/verify.<lang>.*` for the message's `Lang` or the request's
  language (`mail.Languages(ctx)`: the `i18n` locale, else
  `Accept-Language`), and delivers through
  `MAIL_PROVIDER` (`mailgun`, `sendgrid`, `postmark`, `resend`, `smtp`;
  `log` by default, `outbox` in tests). With the `db` pack every message
  is a row in `mail_message` (`Outbox(ctx, n)`, `WaitFor(ctx, to,
  subject, timeout)` in tests, the MCP tool `lidza_mail`); with the
  `jobs` pack delivery runs as a job with
  retries. Never import a vendor SDK (`lidza check` L006).
- `llm`: `llm.From(ctx).Chat(ctx, llm.Request{System: s, Messages:
  []llm.Message{ {Role: llm.User, Content: text} }})` returns `Text` and
  `Usage`; `Stream` delivers the text as it arrives; `llm.Generate[T]`
  sends T's JSON Schema (a `type` from `schema.lidza`) and validates the
  reply; `Run(ctx, req, tools())` offers the app's `lidza.Tool` values to
  the model and runs the calls it makes; `Embeddings(ctx,
  llm.EmbedRequest{Texts: texts, Label: "post.index"})` returns vectors
  and their tokens (`Embed(ctx, texts)` without a label). Providers
  spoken directly: `anthropic`, `openai`, `google`, `ollama` (local),
  `compatible` (any server speaking the OpenAI API), and `fake` for
  tests and a first run (`LLM_PROVIDER`, `LLM_MODEL`, `LLM_API_KEY`,
  `LLM_BASE_URL`). Embeddings use the chat provider unless
  `EMBED_PROVIDER` names another (`openai`, `google`, `ollama`,
  `compatible`, `fake`, or `none` for off; `EMBED_MODEL`,
  `EMBED_API_KEY`, `EMBED_BASE_URL`; `LLM_EMBED_MODEL` is still read
  when `EMBED_MODEL` is empty); Anthropic cannot embed, and Embed says
  to set `EMBED_PROVIDER`. With the db pack every chat and embedding is
  a row in `llm_usage` with its label and tokens. Every call has a
  deadline and retries on 429 and 5xx; tokens count on `/metrics`.
  Never import a vendor SDK or a client library (`lidza check` L007).
  Recipe: "Add an LLM feature".
- `storage`: `storage.From(ctx).Put(ctx, "avatars/"+id+".png", r,
  storage.PutOptions{})` stores a file (content type detected), `Get`,
  `Stat`, `List(ctx, prefix, n)`, `Delete`; `PresignGet(ctx, key, ttl)`
  and `PresignPut` give a browser a URL to read or upload directly,
  `URL(key)` the public address with `STORAGE_PUBLIC_URL`; mount
  `storage.Handler("/api/v1/files/")` behind `auth.Require()` to serve
  private objects through the app. Providers: `s3` (AWS S3, MinIO, R2,
  B2, Wasabi, Spaces: `STORAGE_BUCKET`, `STORAGE_ENDPOINT`, the keys in
  the credentials) and `local` (a directory, for development and tests).
  In a bucket several apps share, `STORAGE_PREFIX=myapp` puts every key
  under `myapp/`; the app's keys never carry it (`Put(ctx,
  "avatars/1.png", ...)` stores `myapp/avatars/1.png`), and `List`,
  `Stat` and `Put` return keys without it.
  Never import a storage SDK (`lidza check` L008) and never write an
  upload or a generated file to the local disk (`os.WriteFile`, L009):
  a node's disk is neither shared nor kept. The MCP tool `lidza_storage`
  lists what is stored. Recipe: "Store a file".
- `admin`: `admin.Mount(r, admin.Options{Title: "notes"})` serves
  the admin pages at `/admin` for `ADMIN_USERS` (see "Admin pages").
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

## Admin pages

`admin.Mount(r, admin.Options{Title: "notes"})` in `app/routes.go` serves
`/admin` for the app's first account (the first user ever to sign in is
an admin; `Options.NoFirstUserAdmin` turns that off; with
`AUTH_OWNER_CLAIM=true`, for a deployment anyone can reach first, it is
the signed-in account that pastes a one-time token on the page `/admin`
shows it: the token is `AUTH_OWNER_CLAIM_TOKEN` from the platform or
generated into `config/owner-claim/token`, with `status.json` beside it
for a hosting agent; it works once and ownership never reopens; `lidza
admin owner status|rotate`) and for the users
`ADMIN_USERS` names (ids or emails, comma separated, in `.env` or added
from the Overview page, which saves them sealed; `Options.Allow` for
another rule). The pages are built on Tabler, light and dark, with their
stylesheet, script and icons served from the binary:

- Overview: each pack's state, a checklist before production, admins.
- Users: every account that signed in, searchable, with sign out
  everywhere, disable, enable and admin rights; a Sign-in tab for the
  providers of `auth.Mount`.
- Mail, Language model, Storage: the outbox, token usage by day and
  label, stored files, each with a Settings tab whose fields follow the
  chosen provider (the Language model tab has two: the model provider,
  and Embeddings with its own).
- Jobs: counts by state, recent jobs, retry.
- The app's own pages (`Options.Pages`) and settings (`Options.Sections`,
  on a Settings page); recipe "Extend the admin pages".

Values saved there are sealed with the master key and applied without a
restart. Theme (read from `Options.Templates` when the app embeds
`admin/`, else from disk): `admin/theme.css` sets the `--admin-*` variables (accent,
background, surface, line, text, sidebar, font, radius; per theme) or any
Tabler variable; `admin/layout.html` replaces the frame (define `layout`,
call `{{template "admin-head" .}}` in `<head>`,
`{{template "admin-scripts" .}}` before `</body>`, and
`{{template "content" .}}` where the page goes). Recipes:
"Add the admin pages", "Extend the admin pages".

## Settings files

Settings are read in layers, each overriding the one before: `.env`,
then `.env.<mode>`, then the credentials, then the process environment.
The mode is `dev` under `lidza dev`, `test` under `lidza test` and
`production` otherwise. `.env` is shared by every mode, tests included,
so:

- a development-only setting (a real provider, a slow import, a
  verbose log) goes in `.env.dev`, never `.env`;
- a test setting goes in `.env.test`, which overrides `.env` (the fake
  model, local storage, the mail outbox, `CACHE_URL=memory`).

Tests never read the sealed credentials file: it holds the deployment's
real keys, and a test must not reach a real service with them. A test
that needs a key sets it with `t.Setenv` (against a stub) or in
`.env.test`; values the app saves at runtime (the admin pages) still
count.

`.env` and `.env.dev` stay out of git. `.env.test` is committed: it
holds no secrets, and the tests and CI need it. Its `DATABASE_URL`
names this machine's Postgres socket; `lidza gen`, `dev`, `test` and
`update` repair it on a machine that keeps the socket elsewhere, and
CI sets its own `DATABASE_URL`.

## Security headers

Every response carries `X-Content-Type-Options: nosniff`,
`X-Frame-Options: DENY`, `Referrer-Policy:
strict-origin-when-cross-origin` and `Permissions-Policy: camera=(),
microphone=(), geolocation=()`. `lidza.App` in `main.go` sets the rest:

- `CSP`: the Content-Security-Policy. Empty sends
  `middleware.DefaultCSP` everywhere but `lidza dev` (Vite's dev server
  injects inline scripts, so dev sends none): scripts, styles, fonts,
  fetches and WebSockets from the app's own origin only, images also from
  `data:` and `blob:`, no plugins, no framing, forms posting to the app.
  The built pages hold under it: the binary adds the hash of each inline
  script and `<style>` a page carries (the router's hydration payload) to
  the header it serves the page with, and JSON-LD from `Head` is data the
  policy does not govern. An app that loads from another origin (a
  script CDN, an image host, a payment form) extends the default rather
  than appending to it, since a browser keeps only the first of a
  repeated directive:
  `CSP: middleware.AddCSP(middleware.DefaultCSP, "script-src", "https://js.example.com")`,
  one call per directive. `middleware.NoCSP` sends none. Inline
  `<script>`, `style=` attributes and `on...=` handlers in hand-written
  HTML are refused; put the code in a file.
- `PermissionsPolicy`: replaces the default. A page that asks for the
  visitor's location (`navigator.geolocation`) needs
  `PermissionsPolicy: middleware.AllowGeolocation` (`geolocation=(self)`,
  camera and microphone still off); with the default the browser refuses
  without showing the prompt. Name any other feature the same way
  (`camera=(self)`) and keep the rest off.
- `HSTS`: the Strict-Transport-Security header. Empty sends
  `middleware.DefaultHSTS` (`max-age=31536000`: browsers keep to HTTPS
  for a year) wherever `APP_URL` is `https://` outside `lidza dev`, which
  `LIDZA_TLS_DOMAINS` sets, and a deploy behind a TLS proxy sets in its
  environment; nothing over HTTP. `middleware.HSTSSubdomains` covers
  every subdomain too: only when the app owns the whole domain.
  `middleware.NoHSTS` sends none.

## Settings and secrets

An app setting (API keys, the providers and models it uses, `APP_URL`,
`MAIL_FROM`, buckets, `ADMIN_USERS`) can come from the environment, the
sealed credentials file or the app's own database, and the environment
wins. Kept in the database, sealed with the master key, a setting is
switched on the admin pages (the pack pages and Settings) with no
restart and no server login, reaches every node, and moves with the
database; `lidza settings set NAME=value` (MCP: `lidza_settings_set`)
does it from a terminal, `lidza settings list` says where each value
comes from (never the value), and `lidza settings import` copies the
values the environment and the files hold into the database (with
`--production`, production's). The deployment's own settings (the
database, the master key, addresses, logging, tracing, tuning) stay in
the environment; `docs/configuration.md` marks each setting `app` or
`infra`. `AUTH_SECRET` is generated on the app's first start and kept
in the database, the same on every node.

Development is seeded by `config/credentials.yml.enc`, sealed with
AES-256-GCM under `config/master.key`, which git ignores: `lidza
credentials set dev.STRIPE_SECRET_KEY=sk_test_...` (MCP:
`lidza_credentials_set`) keeps a sandbox key for `lidza dev` without a
database round; `list` shows the names, `show` every value, `edit` the
file in `$EDITOR`. Values saved in the database win over the file, and
`lidza credentials unset NAME` clears one there too, the way out when a
saved setting stops the app from starting (the start error names them).
Development has defaults for `APP_URL` (`http://` and `LIDZA_ADDR`) and
`MAIL_FROM` (`dev@localhost`); `.env.test` carries the test suite's
fixtures. A key, a token or a database URL with its password pasted
into the Go or frontend source fails `lidza check` (L010).

## Models and migrations

A `model` in `schema.lidza` is a table; a `type` is an API shape only;
an `enum Status { todo doing done }` is a Postgres enum type and a string
union in the clients. Field types: `string`, `text`, `int`, `bigint`,
`float`, `decimal(p, s)`, `bool`, `time`, `date`, `uuid`, `json`,
`bytes`, and in types only `localtime`; `?` makes a field optional (nullable), `[]` an array. Field
attributes: `@id`, `@default(uuid())` / `@default(now())` /
`@default(autoincrement())` / `@default(value)`, `@unique`,
`@index`, `@ref(Model)` for a foreign key, the field typed as the
model's id (`categoryId int @ref(Category)` when `Category` has
`id int @id`; `@ref(Model, cascade)` deletes the row with the
referenced one, `@ref(Model, setnull)` clears an optional field), `@min(n)` / `@max(n)` (length or value), `@email`,
`@url`, `@pattern("re")`, `@timezone` (an IANA zone name), `@search`
(the field is part of the model's full-text search; `@search(A)` to
`@search(D)` weigh it, else by position). Block attributes inside a model:
`@@unique(a, b)`, `@@index(a, b)`, `@@search("english")` (the search's
language: stemming, stop words; `simple`, the default, matches words as
they are); `@table("name")` after the model name.

Numbered rows and exact amounts:

```
model Order {
  id         bigint         @id @default(autoincrement())
  customerId bigint         @ref(Customer)
  total      decimal(12, 2) @min(0) @max(99999999.99)
  discount   decimal(5, 4)?
}
```

- `@default(autoincrement())` on an `int` or `bigint` is a Postgres
  identity column (`GENERATED BY DEFAULT AS IDENTITY`): the database
  numbers the rows, inserts leave it out (the resource generator does),
  and a `@ref` to the model uses the id's type (`bigint` here). Turning
  an existing column into one moves the sequence past its largest value.
- `decimal(p, s)` is `numeric(p,s)`: `p` digits in all, `s` after the
  point, so `decimal(12, 2)` holds up to 9999999999.99. Use it for money
  and quantities, never `float`. In Go it is `decimal.Decimal`
  (`github.com/agim/lidza/pkg/decimal`), a string-backed type: the value
  stays the text Postgres sent, "12.50", with no float rounding, and
  JSON carries it as a string. A literal converts (`Total: "12.50"`);
  outside text goes through `decimal.Parse`; `Cmp` compares exactly;
  arithmetic goes through `math/big` (`d.Rat()`, then
  `decimal.FromRat(r, 2)`, which rounds half away from zero like
  Postgres). It is a type of the lidza module rather than a third-party
  decimal library because the module has none and the framework only
  needs exact transport, comparison and validation; an app that does
  heavy arithmetic converts at the edge. `lidza gen` maps sqlc's numeric
  to the same type in `sqlc.yaml` (between `# lidza gen` comments), so
  `db/queries/gen` and `schema/` agree. pgx reads a zero as `0` whatever
  its scale; every other value keeps its trailing zeros.
- `Validate` checks that a decimal fits its column (rule `decimal`: no
  more digits than `p` and `s` allow, which Postgres would round or
  refuse) and applies `@min` and `@max` exactly, as decimals.
- In TypeScript a decimal is a `string` ("12.50"); `validators.ts`
  applies the same rules with BigInt, never a float. Show it with
  `Number(v).toLocaleString(undefined, { minimumFractionDigits: 2 })` or
  `Intl.NumberFormat(..., { style: 'currency', currency })` for display
  only; send it back as the string the user typed or the server sent,
  and do arithmetic on the server. Dart and Rust carry it as `String`.

Go names follow the field names with initialisms in capitals and their
plurals with a lowercase s: `authorId` is `AuthorID`, `url` is `URL`,
`tagIds` is `TagIDs`. sqlc follows the same rule in
`db/queries/gen`, so a field has the same name in both, and its struct
and enum names come from the table and type names the same way
(`api_key` is `APIKey`): `lidza gen` keeps the initialisms and
renames in `sqlc.yaml` between two `# lidza gen` comments.

`lidza gen` writes the full DDL to
`db/schema.sql` and, when models changed since `db/schema.lock.json`, a
pair in `db/migrations/` named by the UTC time it was written
(`20261006140512_create_post.up.sql`, `.down.sql`), always after the
newest one there; migrations of older releases (`0105_x`) keep their
numbers and sort first. Two branches that each change the schema write
two names, never one, and both apply after the merge.
Statements that lose data or can fail on existing rows carry a
`-- review` comment. Applying migrations is the `db` pack's job.

A step the schema cannot say (a data backfill, a hand-tuned index) is a
migration of its own, never SQL appended to a generated file:
`lidza db new "backfill post slugs"` (MCP `lidza_db_new`) writes an empty
pair named after the newest migration, which `lidza gen` never touches;
write its SQL, then `lidza db migrate`. Make a backfill safe to run
twice (`UPDATE post SET slug = ... WHERE slug IS NULL`).
## Operations

The binary serves `/healthz` (liveness), `/readyz` (503 while a pack's
check fails: database ping, bus) and `/metrics` (Prometheus: requests by
route pattern, durations, pool and connection gauges). In dev,
`/debug/pprof/` too. Tracing is OpenTelemetry, on when
`OTEL_EXPORTER_OTLP_ENDPOINT` is set: spans for requests, queries (by
sqlc name), jobs (continuing the enqueuing request's trace), mail, LLM
calls and `lidza.HTTPClient(ctx)` calls; log with `slog.InfoContext(ctx,
...)` (or `lidza.Log(ctx)`) so a line carries `trace_id`. Give a slow
piece of your own a span with `tracing.Span(ctx, "name")` and
`tracing.End(span, err)` (`github.com/agim/lidza/pkg/tracing`). `lidza check` has rules of its own: package-level
maps or slices (L001) and goroutines started in handlers (L002), because
state belongs in Postgres or Valkey and background work in a bounded
worker or the jobs pack; hand-written `fetch` of `/api` (L003); imports
of packages that do not exist or are not declared (L004); handler types
not declared in `schema.lidza` (L005); a call whose error is dropped, as
a statement or assigned to `_` (L016: handle it or return it; deferred
calls, a `Close` before a `return`, printing to the terminal and writes
to a buffer or a hash are exempt); and a run of at least 8 statements
that repeats another in the app, names and literals aside (L017: reuse
the first or extract a function both call). `// lidza:ignore L016` on
the line, or the line before, exempts one call; above a function,
`// lidza:ignore L017` exempts the function. A form whose `action` or
`formaction` is `mailto:`, `http:` or another non-https scheme warns
(L022): an https page reports it as mixed content on load. An email
form submits to a same-origin handler that sends with the mail pack (or
keeps a same-origin action and handles the submit in script); a
`mailto:` link is fine. `lidza audit performance` checks the rendered
forms the same way, and over https reports the browser's own
mixed-content issues.
Rate limit a route group with `r.Use(middleware.RateLimit(middleware.RateLimitOptions{RPS: 10, Burst: 20}))`
(per node; a limit across nodes counts with the cache pack's `Incr`);
guard an outbound dependency with `resilience.New(...)`.

## Deployment

Managed deploys from a portal: [Līdza Deploy](https://github.com/agim/lidza-deploy)
deploys this app from GitHub on push to a server it manages (HTTPS,
health checks, logs, rollback, team roles); behind its proxy, set
`LIDZA_TRUSTED_PROXIES=loopback`. To run the binary yourself:

`lidza build` makes `bin/notes`: the frontend embedded, no Node at
runtime (except `LIDZA_SSR=1`), with a Brotli and a gzip copy of each
compressible file it serves to browsers that accept one (pages built
per request are gzipped as they go out; `docs/deploy.md` has the
details and the proxy alternative). `Dockerfile` builds the same into an
image that runs as a non-root user on port 3000 (the binary, `db/`,
`mail/`, `admin/` and the sealed credentials; the master key comes from
`LIDZA_MASTER_KEY` in the environment, and the local storage provider
needs a volume at `STORAGE_DIR`, so production uses `s3`); `deploy/notes.service`
runs the binary under systemd from `/opt/notes` (install commands in
its header); `lidza gen deploy --k8s` adds Kubernetes manifests
(`deploy/k8s/notes.yaml`: Deployment, Service, PodDisruptionBudget). Migrations ship as files in `db/`: apply them with `lidza db
migrate --production` in the deploy step or `DB_MIGRATE=true` at start.
They run while the app serves: statements wait at most
`DB_MIGRATE_LOCK_TIMEOUT` (5s) for a lock and retry; `lidza gen` builds
indexes and validates constraints on existing tables in a second,
`-- lidza:no-transaction` migration that runs outside a transaction;
`lidza check` warns (L021) on a new migration that renames, changes a
type, drops what the running version reads, or locks a table for a
scan, and names the expand-and-contract steps. `lidza ship
--domains app.example.com` records the domain in `lidza.json` and writes
`deploy/production.env`, the environment of the deployed process
(plus `DATABASE_URL` and `LIDZA_MASTER_KEY`). TLS: with
`LIDZA_TLS_DOMAINS=app.example.com` the binary serves HTTPS on 443
itself with Let's Encrypt certificates, renewed on its own and stored
in Postgres through the db pack so every node shares them, and
redirects 80; `APP_URL` and `AUTH_COOKIE_SECURE` follow from it. Needs
a DNS record for the domain and ports 80 and 443 open (`lidza doctor`
checks both). Hostnames the app learns while it runs (a customer's own
domain) are approved by `App.TLSHosts` (`func(ctx, host) error`, nil
approves): asked in the handshake, the ACME challenges and before every
certificate order, cached per node (an approval 5 minutes, a refusal 1
minute), so a host it stops approving is refused and never renewed. An
approved host reaches the whole app: serve its paths with `r.Mount`
(outside `/api`) and keep the rest to the app's domains with a
middleware on `r.Host`. The admin overview lists the certificates and
the latest refusals. A terminator in front that issues certificates on
demand (Caddy's `ask`) asks `GET /tls-allow?domain=<host>`: 200 when
`App.TLSHosts` approves, 404 when not, same cache. Or put a TLS-terminating proxy in front, forward
`X-Forwarded-For`, and name it in `LIDZA_TRUSTED_PROXIES` (`loopback`
for a proxy on the same host, else its addresses or CIDRs): only then is
the forwarded client trusted, and the rate limits, the sign-in throttle,
`middleware.ClientIP` and the request log's `client_ip` see each visitor
instead of the proxy. Never read `X-Forwarded-For` by hand. Point the orchestrator at `/healthz` and `/readyz`,
scrape `/metrics`. Production settings: `LIDZA_MODE` unset,
`LIDZA_LOG=json`, `AUTH_SECRET` the same on every node. The framework's
`docs/deploy.md` has the details.

## Environment the binary reads

| Variable | Meaning | Default |
|---|---|---|
| `LIDZA_ADDR` | listen address (ignored when TLS is on) | `127.0.0.1:3000` |
| `LIDZA_TRUSTED_PROXIES` | the proxies in front whose `X-Forwarded-For` counts: IPs, CIDRs, `loopback`, `private`; `middleware.ClientIP`, the rate limits, the sign-in throttle and the request log's `client_ip` resolve the visitor through them | unset (the connection's address; no forwarded header trusted) |
| `AUTH_PROVIDERS` | sign-in providers `auth.Mount` serves: `google`, `github`, `microsoft`, or a name with `AUTH_<NAME>_ISSUER`; each with `AUTH_<NAME>_CLIENT_ID` and `AUTH_<NAME>_CLIENT_SECRET` in the credentials | unset |
| `APP_URL` | the public origin: mail links and the providers' callback URL; `https://` also sends Strict-Transport-Security (`App.HSTS`) | from `LIDZA_TLS_DOMAINS`, else the request |
| `SECURITY_CONTACT` | where to report a vulnerability, served as `/.well-known/security.txt` (RFC 9116, with `Expires` kept ahead): emails or `https://`/`tel:` URLs, comma-separated; unset serves the build's own `.well-known/security.txt`, if any | unset |
| `SECURITY_POLICY` | an `https://` page on how reports are handled, the file's `Policy` | unset |
| `LIDZA_TLS_DOMAINS` | domains to serve over HTTPS with Let's Encrypt certificates; the first is the public name (`APP_URL` when unset; `AUTH_COOKIE_SECURE` becomes true) | unset (plain HTTP) |
| `LIDZA_TLS_EMAIL` | ACME account contact | unset |
| `LIDZA_TLS_ADDR`, `LIDZA_TLS_HTTP_ADDR` | the HTTPS and HTTP (redirect) listen addresses | `:443`, `:80` |
| `LIDZA_TLS_CACHE_DIR` | certificate store on disk for a single node without the db pack | unset (Postgres through the db pack) |
| `LIDZA_TLS_DIRECTORY` | ACME directory URL (Let's Encrypt staging for a rehearsal) | Let's Encrypt |
| `LIDZA_MODE` | `dev` proxies the frontend instead of serving the embedded build | unset (production) |
| `LIDZA_FRONTEND_URL` | dev server to proxy to; set by `lidza dev` | |
| `LIDZA_SSR` | `1` starts the Node SSR sidecar from `dist/.server` (react template) | unset |
| `LIDZA_MCP_TOKEN` | enables `/mcp` (the app's tools over Streamable HTTP) for clients sending it as a bearer token | unset (endpoint off) |
| `LIDZA_LOG` | log format, `json` or `text` | `text` under `lidza dev` and `lidza test`, else `json` |
| `LIDZA_LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` |
