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

## 2026-09-25: Tag suggestions through the llm pack

Why: The model is reached only through the pack (`llm.Generate` over a schema type), so the reply is validated by the type's rules and tests script the fake provider instead of calling a vendor; the prompt stays in the handler.

Touches: handlers/tags.go, schema.lidza (NoteTags), .env.test (LLM_PROVIDER=fake)

## 2026-09-26: Pack db added

Why: Notes are rows: Postgres through the db pack, migrations from schema.lidza, typed queries with sqlc.

Touches: lidza.json, db/

## 2026-09-26: Pack auth added

Why: Accounts with sessions, verification and reset come from the auth pack instead of a hand-written login.

Touches: lidza.json, handlers/auth.go

## 2026-09-26: Pack storage added

Why: A note's attachment lives in object storage through the storage pack: local files in development and tests, S3 in production, never the node's disk.

Touches: lidza.json, handlers/attachment.go

## 2026-09-26: Pack mail added

Why: Verification and reset links go out through the mail pack: providers spoken directly, an outbox the tests read.

Touches: lidza.json, mail/
