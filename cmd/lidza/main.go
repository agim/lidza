// Command lidza is the Līdza CLI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/agim/lidza/pkg/devserver"
	"github.com/agim/lidza/pkg/version"
)

const usage = `Līdza: Go control plane, Rust core, any frontend.

Usage:
  lidza new <name> [--template react] [--packs db,auth,mail] [--agent claude] [--no-setup]
  lidza install [--packs db,auth,mail] [--agent claude] [--database-url ...] [--no-commit] [--migrate]
  lidza ship [--no-e2e] [--out bin/<name>]
  lidza dev   [--dir .] [--addr 127.0.0.1:3000]
  lidza build [--dir .] [--out bin/<name>]
  lidza check [--dir .] [--json]
  lidza gen [--dir .] | lidza gen resource <Model> [--force] [--public | --shared] | lidza gen llms [--force]
  lidza pack add|scaffold|build|list [name]
  lidza db migrate|rollback|status | lidza db new "<description>"
  lidza benchmark [scenario] [--vus 500] [--duration 1m]
  lidza test [--fresh] [go test flags] | lidza test --e2e [--install]
  lidza verify [--json] [--no-test] [--strict] | lidza verify --install-hook
  lidza doctor
  lidza audit layout [--viewport 1440x900,390x844] [--theme light,dark] [--max-scroll -1]
  lidza update [--to vX.Y.Z|<commit>] [--cli-only] [--commit] [--migrate] [--no-pull] [--allow-behind] [--allow-dirty]
  lidza context [--dir .] [--stdout]
  lidza api [package] [--filter name] [--list]
  lidza snippet [name]
  lidza recipe add "<title>" [--description ...] [--step ...] | lidza recipe list
  lidza decision add "<title>" --why "..." [--touches ...] | lidza decision list
  lidza credentials init | set NAME=value ... | unset NAME ... | list | show [NAME] | edit
  lidza admin add EMAIL... | remove EMAIL... | list
  lidza mcp [--dir .]
  lidza version

Commands:
  new      create an app from a template, then set it up (packs, .env with a random AUTH_SECRET, databases created and migrated, node_modules, first commit)
  install  the same on an existing app, a fresh clone or after a pull (setup is its former name): enable packs, write .env and .env.test and add what enabled packs need, generate, create and migrate the databases (a migration dropping data waits for --migrate), npm install, install an agent CLI, commit
  ship     verify, the browser suite, the production build: what must be green before a deploy
  dev      run the app with hot reload (frontend dev server proxied behind /api)
  build    build the frontend and compile one production binary
  check    run go vet, staticcheck, cargo check and tsc; one diagnostics list
  gen      generate from schema.lidza (Go, SQL, migrations, Rust), the packs and the handlers (OpenAPI, @lidza/client);
           gen resource <Model>: queries, Create/Update types, handlers and routes for a model,
             signed-in and scoped to its owner (ownerId, userId, @ref(User)) unless --public;
           gen llms: a public llms.txt to fill in (name, summary, a link per prerendered page)
  pack     add official packs (db, realtime, media, geo), scaffold, build and list local ones
  db       apply, revert and list migrations (lidza/db pack)
  benchmark  run a k6 scenario from benchmarks/ against the running app; heap before and after
  test     go test ./... with LIDZA_MODE=test, the test database created and migrated, then the frontend check; --e2e runs the Playwright suite against the built binary
  verify   before a commit: regenerate (generated files must be staged), check, go test; the pre-commit hook runs it
  audit    layout: every page at each viewport and theme, signed in, for what scrolls sideways (a fault) or down;
           performance: every page cold on a throttled phone: load times, bytes, unused JS, against budgets
  doctor   report the toolchain, services and the project's prerequisites, each with its fix
  context  write .lidza/context.json: routes, handler signatures, Rust exports
  api      print the framework's public Go API as the project resolves it: the package list, one package, a --filter search, or all
  recipe   add one of this app's conventions to docs/lidza-guide.md as a recipe (prompt, skills, command), or list the recipes
  credentials the app's secrets, sealed in config/credentials.yml.enc with config/master.key; every pack reads them like .env; dev.NAME and production.NAME are for one mode
  admin    who may open the admin pages besides the first account: ADMIN_USERS in the credentials, read within seconds
  update   in a project, first the branch pulled when it is a clean fast-forward; then the CLI to the newest release and the module to the same version: go get, tidy, Dockerfile pin, lidza gen, then lidza install
  decision record why the app is built a way (a pack, Rust, a dependency, a schema tradeoff) in docs/decisions.md, or list the decisions
  brief    the kickoff interview: what the app is for, who owns the data, the design, the services, the working agreements; answers go to docs/brief.md and where they act
  note     add a lasting fact about this app for the team and every agent (the agent files' Team notes)
  snippet  print a file of the reference app (examples/notes): auth routes, an owned resource, a page, tests, a tool
  mcp      serve routes, context, diagnostics, dev logs, the API and the guide's recipes over MCP on stdio
  version  print the framework version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// The CLI works on a development checkout: it reads .env.dev and the
	// credentials' dev: section, never production's (a sealed production
	// DATABASE_URL). lidza test and verify set test; ship resolves
	// production itself.
	if os.Getenv(devserver.EnvMode) == "" {
		os.Setenv(devserver.EnvMode, "dev")
	}

	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "new":
		err = runNew(ctx, args)
	case "install", "setup":
		err = runInstall(ctx, args)
	case "ship":
		err = runShip(ctx, args)
	case "dev":
		err = runDev(ctx, args)
	case "build":
		err = runBuild(ctx, args)
	case "check":
		err = runCheck(ctx, args)
	case "gen":
		err = runGen(ctx, args)
	case "pack":
		err = runPack(ctx, args)
	case "db":
		err = runDB(ctx, args)
	case "benchmark":
		err = runBenchmark(ctx, args)
	case "test":
		err = runTest(ctx, args)
	case "verify":
		err = runVerify(ctx, args)
	case "audit":
		err = runAudit(ctx, args)
	case "doctor":
		err = runDoctor(ctx, args)
	case "context":
		err = runContext(ctx, args)
	case "api":
		err = runAPI(ctx, args)
	case "snippet":
		err = runSnippet(ctx, args)
	case "update":
		err = runUpdate(ctx, args)
	case "credentials":
		err = runCredentials(ctx, args)
	case "admin":
		err = runAdmin(ctx, args)
	case "decision":
		err = runDecision(ctx, args)
	case "brief":
		err = runBrief(ctx, args)
	case "note":
		err = runNote(ctx, args)
	case "recipe":
		err = runRecipe(ctx, args)
	case "mcp":
		err = runMCP(ctx, args)
	case "version", "--version", "-v":
		fmt.Println("lidza", version.String())
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "lidza: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if errors.Is(err, errCheckFailed) {
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "lidza:", err)
		os.Exit(1)
	}
}

// flags returns a FlagSet that prints its own usage on error and exits.
func flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("lidza "+name, flag.ExitOnError)
	return fs
}
