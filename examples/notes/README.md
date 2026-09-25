# notes

The reference Līdza application: registration and login with the `auth`
pack, a `Note` resource owned by the signed-in user, a React page that
uses only the generated client, a handler test, a browser test and an
MCP tool. `lidza mcp` serves its files as snippets (`lidza_snippet`) in
every app, so an agent copies working code instead of guessing.

Run it:

```sh
cd examples/notes
cp .env.example .env          # set DATABASE_URL and AUTH_SECRET
lidza db migrate
lidza dev                     # http://127.0.0.1:3000
lidza test                    # handler tests against .env.test
lidza test --e2e              # the browser test against the built binary
```

What to read, in order:

| File | Shows |
|---|---|
| `schema.lidza` | models (`User`, `Note`), API types with rules (`Credentials`, `CreateNote`) |
| `routes.go` | public routes, a group behind `auth.Require()`, the resource mounted on a group |
| `handlers/auth.go` | register, login, logout, me: cookies for browsers, a bearer token for other clients |
| `handlers/note.go` | the generated resource scoped to the owner |
| `db/queries/note.sql` | sqlc queries with owner checks |
| `routes_test.go` | `lidzatest.Start`, cookies across calls, validation and authorization failures |
| `tools.go` | an MCP tool that runs inside the app |
| `src/pages/Home.tsx` | `api.*` and `validators.*` from `@lidza/client`, sign-in and notes |
| `e2e/notes.spec.ts` | a Playwright test that also asserts no window errors |
