# Decisions

What shaped notes and why, one entry each, newest last: a pack added, Rust
chosen for a module, a dependency taken, a schema tradeoff, an
integration. Read this before working in those areas. Record yours in the
same commit as the change: `lidza decision add "Title" --why "..."`
(MCP: `lidza_decision_add`). A recipe in docs/lidza-guide.md says how
this app does something; a decision here says why it is done that way.

## 2026-09-25: Word statistics in a Rust pack

Why: The statistics run over text users typed, so the loop is contained in the WASM sandbox with a memory cap and a deadline; Go would do the arithmetic faster, which is not the point (see "Rust: when and how" in the guide).

Touches: packs/stats, handlers/note.go (noteStats)

## 2026-09-25: Mail through the outbox in tests

Why: Tests read verification and reset links from the outbox table instead of a mail stub, so the same code path runs in tests and in production; `.env.test` sets `MAIL_PROVIDER=outbox`.

Touches: .env.test, routes_test.go
