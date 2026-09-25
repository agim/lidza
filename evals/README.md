# Platform evals

What the platform does for an agent, checked end to end on a fresh app:

| Case | Expectation |
|---|---|
| a fresh app | `lidza check` reports nothing |
| a hand-written `fetch('/api/...')` | L003 warning naming `@lidza/client` |
| an import `package.json` does not declare | L004 error with the `npm install` line |
| an import of a framework package that does not exist | L004 error, and the go tool's `go get` advice is gone |
| a handler with types outside `schema.lidza` | L005 warning per type |
| a package-level map, a goroutine in a handler | L001 and L002 warnings |
| a frontend call to an operation that does not exist | TS2339 error in the page |
| a field renamed in `schema.lidza` | TS2339 error in the page that reads the old name |
| an `<img>` without `alt` | `jsx-a11y/alt-text` error |
| guidance | the six skills, the agent files naming them and the tools, the pre-commit hook, `lidza api`, `lidza snippet`, and the MCP prompts, tools and `lidza://api` |
| `lidza verify` | passes on the fresh app, tests included |
| `examples/notes` | its own `lidza verify` passes (needs Postgres; skipped otherwise) |

Run:

```sh
go test -tags evals ./evals -v
```

The suite builds the CLI from this checkout, scaffolds an app in a
temporary directory with `--lidza-dir` pointing here and runs `npm
install` once; each case edits the app and restores it. A few minutes.
Add a case for every new rule or guidance surface.
