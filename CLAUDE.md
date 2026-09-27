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
- Nested agent runs (the agent eval) use `claude -p --permission-mode
  acceptEdits` with an allow-list, not `--dangerously-skip-permissions`.
