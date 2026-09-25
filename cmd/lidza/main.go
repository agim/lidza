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

	"github.com/agim/lidza/pkg/version"
)

const usage = `Līdza: Go control plane, Rust core, any frontend.

Usage:
  lidza new <name> [--template react] [--lidza-dir <path>]
  lidza dev   [--dir .] [--addr 127.0.0.1:3000]
  lidza build [--dir .] [--out bin/<name>]
  lidza check [--dir .] [--json]
  lidza gen [--dir .] | lidza gen resource <Model> [--force]
  lidza pack add|scaffold|build|list [name]
  lidza db migrate|rollback|status
  lidza benchmark [scenario] [--vus 500] [--duration 1m]
  lidza test [go test flags] | lidza test --e2e [--install]
  lidza verify [--json] [--no-test] | lidza verify --install-hook
  lidza doctor
  lidza context [--dir .] [--stdout]
  lidza api [package] [--filter name] [--list]
  lidza snippet [name]
  lidza mcp [--dir .]
  lidza version

Commands:
  new      create an app from a template
  dev      run the app with hot reload (frontend dev server proxied behind /api)
  build    build the frontend and compile one production binary
  check    run go vet, staticcheck, cargo check and tsc; one diagnostics list
  gen      generate from schema.lidza (Go, SQL, migrations, Rust), the packs and the handlers (OpenAPI, @lidza/client);
           gen resource <Model>: queries, Create/Update types, handlers and routes for a model
  pack     add official packs (db, realtime, media, geo), scaffold, build and list local ones
  db       apply, revert and list migrations (lidza/db pack)
  benchmark  run a k6 scenario from benchmarks/ against the running app; heap before and after
  test     go test ./... with LIDZA_MODE=test, the test database created and migrated, then the frontend check; --e2e runs the Playwright suite against the built binary
  verify   before a commit: regenerate (generated files must be staged), check, go test; the pre-commit hook runs it
  doctor   report the toolchain, services and the project's prerequisites, each with its fix
  context  write .lidza/context.json: routes, handler signatures, Rust exports
  api      print the framework's public Go API as the project resolves it (one package, or all that app code imports)
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

	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "new":
		err = runNew(ctx, args)
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
	case "doctor":
		err = runDoctor(ctx, args)
	case "context":
		err = runContext(ctx, args)
	case "api":
		err = runAPI(ctx, args)
	case "snippet":
		err = runSnippet(ctx, args)
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
