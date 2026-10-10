# Versioning

Līdza is released as git tags `vX.Y.Z` (`scripts/release.sh`). An app's
`go.mod` and the CLI it runs carry the same version; `lidza update`
moves both forward, never back.

## Public API

A change to any of these can break an app, so it follows the rules
below:

- Go packages an app imports: the root package `lidza`, `packs/*`, and
  `pkg/*` outside the list below.
- The CLI: commands, flags, exit codes and the JSON of `--json` output.
- `schema.lidza` syntax and what it generates: Go types, SQL,
  migrations, OpenAPI, `@lidza/client` and the Dart client.
- `lidza.json`, environment variables and settings the packs read.
- HTTP routes the packs mount, and their request and response shapes.
- `lidza check` rule codes (`L001` and on) and what they mean.
- MCP tool names and their arguments.

Not public: `cmd/lidza` internals, `pkg/scaffold`, `pkg/snippets`,
`pkg/devserver`, `pkg/mcpserver` and `pkg/inspect` Go APIs (used by the
CLI, not by apps), generated file contents beyond their documented
types, and anything marked "experimental" in its doc comment.

## Before v1.0

The version stays `v0.x`. Releases may break, but never silently:

- A breaking change is listed first under its version in
  `CHANGELOG.md` as "Breaking:", with what to change in the app.
- When keeping the old form is cheap (a renamed function, an aliased
  flag), it stays for at least one release with a `Deprecated:` doc
  comment naming the replacement, then goes in a later "Breaking:"
  entry.
- A release without "Breaking:" is additive: `lidza update --migrate`
  takes an app across it with no edits.
- Security fixes may break behaviour that was unsafe (a default that
  allowed too much); the entry says so and why.

## v1.0

v1.0 freezes the public API above. After it, a breaking change needs
`v2`, with its own module path, as Go requires. The work to get there
is the "v1.0: API freeze" item in `docs/roadmap.md`.
