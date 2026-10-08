package app

import (
	"github.com/agim/lidza"
	"io/fs"
)

// app describes the application; tests start it with lidzatest.Start.
func New(dist fs.FS) lidza.App {
	return lidza.App{
		Name:       "notes",
		Dist:       dist,
		Routes:     routes,
		Packs:      packs(),
		Tools:      tools(),
		OnStart:    onStart,
		Middleware: appMiddleware(),
	}
}
