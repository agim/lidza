package main

import (
	"embed"
	"github.com/agim/lidza"
	"notes/app"
)

// The production binary carries the frontend build. `lidza build` fills
// dist/ before compiling; in dev the frontend is proxied instead.
//
//go:embed all:dist
var dist embed.FS

func main() { lidza.Run(app.New(lidza.Sub(dist, "dist"))) }
