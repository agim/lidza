package main

import (
	"embed"
	"github.com/agim/lidza"
)

// The production binary carries the frontend build. `lidza build` fills
// dist/ before compiling; in dev the frontend is proxied instead.
//
//go:embed all:dist
var dist embed.FS

func main() { lidza.Run(app()) }

// app describes the application; tests start it with lidzatest.Start.
func app() lidza.App {
	return lidza.App{
		Name:    "notes",
		Dist:    lidza.Sub(dist, "dist"),
		Routes:  routes,
		Packs:   packs(),
		Tools:   tools(),
		OnStart: onStart,
	}
}
