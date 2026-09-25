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
  lidza gen [--dir .]
  lidza pack scaffold|build|list [name]
  lidza context [--dir .] [--stdout]
  lidza mcp [--dir .]
  lidza version

Commands:
  new      create an app from a template
  dev      run the app with hot reload (frontend dev server proxied behind /api)
  build    build the frontend and compile one production binary
  check    run go vet, staticcheck, cargo check and tsc; one diagnostics list
  gen      generate from schema.lidza (Go, SQL, migrations, Rust), the packs and the handlers (OpenAPI, @lidza/client)
  pack     scaffold, build and list packs (Rust capabilities run as WASM)
  context  write .lidza/context.json: routes, handler signatures, Rust exports
  mcp      serve routes, context, diagnostics and dev logs over MCP on stdio
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
	case "context":
		err = runContext(ctx, args)
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
