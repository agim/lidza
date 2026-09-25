# Līdza: notes for AI agents

Read before changing anything:
- `docs/environment.md`: the host, the toolchain plan and its checks, services, ports, layout, naming. **The environment contract.**
- `docs/roadmap.md`: phases and acceptance checks. Work in phase order.
- `docs/scalability.md`: the rules every package follows from the first
  commit (stateless, bounded, owned goroutines, deadlines everywhere).
- `docs/features.md`: what a complete framework needs, mapped to phases.
- `docs/research/2026-09-lidza-design-notes.md`: where the design came from; not a spec.

Gotchas:
- **Līdza** in prose and docs, **`lidza`** in every identifier, path, package and domain. Never `ī` in code.
- Go and Rust are **not installed yet**. Never install ad hoc; run the steps in `docs/environment.md` only when Agim says go, then tick the verification list.
- Go installs to `/usr/local/go` and needs `sudo` (passwordless). Rust is user-local via rustup.
- Docker: `agim` is not in the `docker` group; use `sg docker -c '...'`.
- Redis on 6379 is the local Valkey stand-in.
- Git: pull before committing, push straight to `master`; no branches or PRs unless asked.
- Keep docs plain: short headings, facts, no decorative arrows, no taglines.
