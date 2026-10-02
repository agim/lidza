# Līdza: notes for AI agents

Read before changing anything:
- `docs/environment.md`: the host, the toolchain plan and its checks, services, ports, layout, naming. **The environment contract.**
- `docs/roadmap.md`: phases and acceptance checks. Work in phase order.
- `docs/scalability.md`: the rules every package follows from the first
  commit (stateless, bounded, owned goroutines, deadlines everywhere).
- `docs/features.md`: what a complete framework needs, mapped to phases.
- `docs/design.md`: why the parts are what they are.

Gotchas:
- **Līdza** in prose and docs, **`lidza`** in every identifier, path, package and domain. Never `ī` in code.
- The toolchain is installed (`lidza doctor`, `sh install.sh --check`). Never install more ad hoc; `docs/environment.md` records every install and its check.
- Go lives in `/usr/local/go` (installed with passwordless `sudo`); Rust is user-local via rustup.
- Docker: `agim` is not in the `docker` group; use `sg docker -c '...'`.
- Redis on 6379 is the local Valkey stand-in.
- `examples/notes` is the reference app; its files are embedded for `lidza snippet` from `pkg/snippets/_files`. After changing the example: `go generate ./pkg/snippets` (the package test fails while they differ), and `lidza test` plus `lidza test --e2e` inside it.
- A new check rule or agent surface gets a case in `evals/` (`go test -tags evals ./evals`).
- Build the CLI with `go install ./cmd/lidza` or `go build -o <scratch path>`, never a bare `go build ./cmd/lidza` at the root: it writes a 36 MB `lidza` there (ignored now; `scripts/release.sh` refuses tracked files over 2 MB).
- Git: pull before committing, push straight to `master`; no branches or PRs unless asked.
- Releases: `install.sh` installs the tag it pins, not master. A new command
  or flag reaches users only once `scripts/release.sh vX.Y.Z` has tagged it,
  so tag before a runbook or the README tells anyone to run it. Entries go
  under "## Unreleased" in `CHANGELOG.md` until then.
  Where tags cannot be pushed (an agent's cloud session),
  `RELEASE_TAG=ci scripts/release.sh vX.Y.Z` pushes master only and
  `.github/workflows/tag.yml` tags the release commit; check the tag exists
  remotely (`git ls-remote --tags origin vX.Y.Z`) before an app updates.
- Keep docs plain: short headings, facts, no decorative arrows, no taglines.

Working agreements (Agim's standing decisions):
- Durable knowledge lives in this repository, pushed for the team: this
  file, `docs/`, `CHANGELOG.md`. No local agent memory for this project.
- Build the framework, not apps: "start building" means the next roadmap
  phase here. Demo and test apps stay in a scratch directory
  (`lidza new demo --lidza-dir "$PWD"`), never in the repo;
  `examples/notes` is the only app in it.
- Fixes go ahead without asking: a bug or gap found in Līdza is fixed,
  tested and released (`scripts/release.sh`), then reported. New features
  and redesigns wait for Agim's go; destructive actions are confirmed.
- Docs and comments carry framework facts only. A host fact that belongs
  to the environment contract goes in `docs/environment.md`; incidental
  findings (another project's port, a probe mishap) stay out of the repo.
- Always neutral: framework code, comments, docs, tests, examples and
  the changelog never name a sample or user app (Galeria, the tracker)
  or borrow its domain (artworks, a curator, museum departments). A gap
  an app found is described as the framework's gap, with neutral
  examples (posts, tags, categories, products).
- No stock palette (Tailwind or Tabler blue, slate or navy): Līdza's
  palette is a mulberry accent on warm neutrals, defined in
  `packs/admin/templates/theme.css` and the templates' tokens; status
  colours stay semantic.
- Rust is not for speed: packs run 1.6 to 3 times slower than Go and
  every call pays about 12 µs plus 14 µs per KB. The case for a pack is
  containment, a crate Go lacks, or heap pressure; the guide's "Rust: when
  and how" keeps the table (rerun `go test ./pkg/engine -bench .` on an
  idle machine).
- Commit `examples/notes` before running the evals: `TestReferenceApp`
  runs `lidza verify` there, which refuses unstaged generated files.
- Never assume the developer's machine: CI runners have no git author,
  macOS keeps the Postgres socket in `/tmp`, Node may be newer than 22.
  Setup and the doctor detect and say, they do not fail silently.
- Decisions for Agim come as brief-style interview questions: each with
  2 to 4 options, the recommended one first and marked, a line on what
  each means; never as open questions buried in a report.
- Nested agent runs (the agent eval) use `claude -p --permission-mode
  acceptEdits` with an allow-list, not `--dangerously-skip-permissions`.

## Shared agent guidance

- `CLAUDE.md`, `AGENTS.md` and `GEMINI.md` carry the same instructions.
  Update all three Markdown files in the same change whenever a rule,
  working agreement or durable note changes. Recipes are edited in the
  guide and regenerated for Claude Code, Codex and Gemini together.
- Application-owned business code belongs in `internal/<feature>/`, vendor
  clients in `internal/providers/<vendor>/`, shared infrastructure in
  `internal/platform/<name>/`. Keep application wiring in `appDir`, handlers
  in `handlers/`, and models and API shapes in `schema.lidza`. Generated paths
  stay fixed. Framework APIs that applications import stay outside `internal/`.

- Queue mail that follows database writes with `mail.SendTx` in the same
  transaction, with db and jobs enabled; roll back on any error. Ordinary
  `Send` owns its transaction and must run outside an app transaction.

- Mail inline files use `Attachment.ContentID` (a bare, unique safe ASCII ID,
  at most 127 bytes) referenced as `cid:<ID>` from HTML. `ReplyTo` accepts an
  address list bounded by `MAIL_MAX_RECIPIENTS`; custom header names are
  unique ignoring case. Keep fields intact through queued retries and test
  MIME nesting plus each provider mapping with local transports.
