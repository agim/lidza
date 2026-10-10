# Contributing

Līdza is a framework for many apps. A change is accepted when it helps
every Līdza app; an app's own edge case belongs in that app, and only a
general primitive or a bug fix inside it comes here.

## Before you start

- Bugs and small fixes: open a pull request, or an issue first if the
  cause is unclear.
- New features and redesigns: open an issue describing the problem
  first. `docs/roadmap.md` lists what is planned; `docs/design.md` says
  why the parts are what they are.
- Vulnerabilities: never in a public issue. `SECURITY.md` says how to
  report one privately.

## Set up

```sh
sh install.sh --check          # what the toolchain needs; docs/environment.md has the list
go build -o bin/lidza ./cmd/lidza
bin/lidza new demo --lidza-dir "$PWD"   # an app built against this checkout, outside the repo
```

Postgres and Valkey run locally for the integration tests
(`LIDZA_TEST_DATABASE_URL`, default the `lidza_test` database over the
Unix socket). Tests that need a service skip without it.

## Checks

Run before you push; CI runs the same:

```sh
gofmt -l . && go vet ./... && staticcheck ./... && go test ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
(cd core && cargo test)
go test -tags evals -timeout 40m ./evals    # when the change touches the CLI, templates, rules or the agent surfaces
```

- A bug fix comes with a test that fails without it.
- A parser of untrusted input gets a fuzz test (`scripts/fuzz.sh` runs
  them all).
- A new `lidza check` rule or agent surface gets a case in `evals/`.
- A new CLI command or official Go pack gets MCP tools, or a reason it
  has none, in `pkg/mcpserver/coverage.go`.
- A new or changed setting is documented on its field;
  `go generate ./pkg/configref` updates `docs/configuration.md`.
- A change to `examples/notes` needs `go generate ./pkg/snippets`.

## Conventions

- **Līdza** in prose, **`lidza`** in every identifier, path and package.
- Docs and comments state framework facts, plainly: short headings, no
  taglines. Examples are neutral (posts, tags, products), never a
  specific app's domain.
- Every package follows `docs/scalability.md`: no unbounded state,
  goroutines owned and stopped, deadlines on every outside call.
- Changes go under "## Unreleased" in `CHANGELOG.md`. A change that
  breaks an app is listed as "Breaking:" with what to change;
  `docs/versioning.md` says what counts as public API.

## License

Līdza is licensed under the Apache License 2.0. A contribution you
submit is licensed under the same terms (section 5 of the license).
