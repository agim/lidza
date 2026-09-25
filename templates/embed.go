// Package templates embeds the app templates that `lidza new` copies.
package templates

import "embed"

// FS holds one directory per template. `all:` keeps dotfiles such as
// .gitignore and dist/.gitkeep. Files ending in .tmpl are written without
// the suffix, which keeps Go sources in a template out of this module's
// build.
//
//go:embed all:react all:svelte all:astro all:htmx
var FS embed.FS

// Names lists the templates that exist.
var Names = []string{"react", "svelte", "astro", "htmx"}
