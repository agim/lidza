// Package templates embeds the app templates that `lidza new` copies.
package templates

import "embed"

// FS holds one directory per template. `all:` keeps dotfiles such as
// .gitignore and dist/.gitkeep.
//
//go:embed all:react
var FS embed.FS

// Names lists the templates that exist. The roadmap adds svelte, astro and
// htmx in Phase 3.
var Names = []string{"react"}
