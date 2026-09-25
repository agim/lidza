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
- Git: pull before committing, push straight to `master`; no branches or PRs unless asked.
- Releases: `install.sh` installs the tag it pins, not master. A new command
  or flag reaches users only once `scripts/release.sh vX.Y.Z` has tagged it,
  so tag before a runbook or the README tells anyone to run it. Entries go
  under "## Unreleased" in `CHANGELOG.md` until then.
- Keep docs plain: short headings, facts, no decorative arrows, no taglines.
