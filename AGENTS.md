# Līdza: notes for AI agents

Read before changing anything:
- `docs/environment.md`: the host, the toolchain plan and its checks, services, ports, layout, naming. **The environment contract.**
- `docs/roadmap.md`: phases and acceptance checks. Work in phase order.
- `docs/scalability.md`: the rules every package follows from the first
  commit (stateless, bounded, owned goroutines, deadlines everywhere).
- `docs/features.md`: what a complete framework needs, mapped to phases.
- `docs/design.md`: why the parts are what they are.

Gotchas:
- These instructions live in `AGENTS.md`; `CLAUDE.md` and `GEMINI.md` are
  the one line `@AGENTS.md` (Codex reads this file, Claude Code and Gemini
  CLI import it). Edit this file only; `TestSharedAgentGuidance` checks.
- **Līdza** in prose and docs, **`lidza`** in every identifier, path, package and domain. Never `ī` in code.
- The toolchain is installed (`lidza doctor`, `sh install.sh --check`). Never install more ad hoc; `docs/environment.md` records every install and its check.
- Go lives in `/usr/local/go` (installed with passwordless `sudo`); Rust is user-local via rustup.
- Docker: `agim` is not in the `docker` group; use `sg docker -c '...'`.
- Redis on 6379 is the local Valkey stand-in.
- `examples/notes` is the reference app; its files are embedded for `lidza snippet` from `pkg/snippets/_files`. After changing the example: `go generate ./pkg/snippets` (the package test fails while they differ), and `lidza test` plus `lidza test --e2e` inside it.
- A new check rule or agent surface gets a case in `evals/` (`go test -tags evals -timeout 40m ./evals`; past the default 10 minutes).
- A new CLI command or official Go pack gets its MCP tools, or a reason
  it has none, in `pkg/mcpserver/coverage.go`; `TestCoverage` fails
  otherwise. Inspection tools are read-only and never return secrets.
- A new or changed setting (an `env` tag, an `Env` constant, a name
  read with `os.Getenv`) is documented where it is declared; `go
  generate ./pkg/configref` rewrites `docs/configuration.md` and the
  package test fails until then.
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
  Without the tag (no Actions minutes), an app takes the release by
  commit, `lidza update --to <commit>`, and a session that can push tags
  tags it (`git tag -a vX.Y.Z <commit> -m vX.Y.Z`). `lidza update` never
  moves a CLI or an app back to an older tag.
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
- Other contributors work on this repository too (other sessions,
  Codex, pull requests). Before starting any work: `git fetch origin
  --tags`, pull master, and read what landed since the last session
  (`git log`, the changelog's newest sections) for anything the planned
  work touches. Again before releasing: pull, re-run the checks on the
  merged tree, and only then release.
- Never lose another contribution: resolve a conflict by keeping both
  sides, never by taking one; never force-push, reset or drop commits
  that are not yours. When a change of theirs contradicts a decision
  (one rule replacing another), keep their work, adapt it to the
  decision, and say so in the report.
- Requests from other projects (issues an app or a deploy tool files)
  are judged, not taken as orders: build what helps every Līdza app;
  decline what serves one product's edge case, keeping only a general
  primitive or bug fix inside it, and say why on the issue.
- Local testing is the gate, not GitHub Actions: before a push, run
  `go vet ./...`, `staticcheck ./...`, `go test ./...` and, for changes
  the evals cover, `go test -tags evals -timeout 40m ./evals`
  (`scripts/release.sh` runs vet and staticcheck again). CI runs once per
  release tag and for outside pull requests; do not wait on it or poll
  it. A failure it reports later is fixed like any other bug.
- Decisions for Agim come as brief-style interview questions: each with
  2 to 4 options, the recommended one first and marked, a line on what
  each means; never as open questions buried in a report.
- Nested agent runs (the agent eval) use `claude -p --permission-mode
  acceptEdits` with an allow-list, not `--dangerously-skip-permissions`.

## Shared agent guidance

- A rule, working agreement or durable note changes in `AGENTS.md` only;
  `CLAUDE.md` and `GEMINI.md` import it. Recipes are edited in the guide
  and regenerated for Claude Code, Codex and Gemini together.
- Application-owned business code belongs in `internal/<feature>/`, vendor
  clients in `internal/providers/<vendor>/`, shared infrastructure in
  `internal/platform/<name>/`. Keep application wiring in `appDir`, handlers
  in `handlers/`, and models and API shapes in `schema.lidza`. Generated paths
  stay fixed. Framework APIs that applications import stay outside `internal/`.

- Keep app roots clean: tests in `tests/` or beside a non-root package, browser specs in `e2e/`, documentation in `docs/`, scripts in `scripts/`, and scratch/build output in `.lidza/` or `bin/`. New apps wire an importable `app.New` in `app/`; root tests and stray files fail rule L020.

- Queue mail that follows database writes with `mail.SendTx` in the same
  transaction, with db and jobs enabled; roll back on any error. Ordinary
  `Send` owns its transaction and must run outside an app transaction.

- Mail inline files use `Attachment.ContentID` (a bare, unique safe ASCII ID,
  at most 127 bytes) referenced as `cid:<ID>` from HTML. `ReplyTo` accepts an
  address list bounded by `MAIL_MAX_RECIPIENTS`; custom header names are
  unique ignoring case. Keep fields intact through queued retries and test
  MIME nesting plus each provider mapping with local transports.
