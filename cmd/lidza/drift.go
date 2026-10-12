package main

import (
	"fmt"
	"io"

	"golang.org/x/mod/semver"

	"github.com/agim/lidza/pkg/scaffold"
)

// drift is this CLI's release against the one the app's go.mod pins.
// The CLI writes the agent files (AGENTS.md, the guide, the skills) from
// its own templates; when the two differ, each machine would commit its
// own version's text, so the writing commands hold back and say so.
type drift struct {
	cli, app string
	newer    bool // the CLI is the newer one
}

// frameworkDrift is nil when the versions match, the CLI is built from
// source, or the app's go.mod points at a local checkout.
func frameworkDrift(dir string) *drift {
	cli := cliRelease()
	if cli == "" {
		return nil
	}
	v := scaffold.AppVersions(dir)
	if v.LocalPath != "" || v.Lidza == "" || v.Lidza == cli {
		return nil
	}
	return &drift{cli: cli, app: v.Lidza, newer: semver.Compare(cli, v.Lidza) > 0}
}

func (d *drift) String() string {
	if d.newer {
		return fmt.Sprintf("this CLI is %s but go.mod pins %s: the agent files (AGENTS.md, the guide, the skills) are left as %s wrote them, so this machine's commits match the others'. `lidza update --cli-only --to %s` uses the app's release; `lidza update` moves the app to %s (one person, then the others pull)",
			d.cli, d.app, d.app, d.app, d.cli)
	}
	return fmt.Sprintf("this CLI is %s, older than the %s go.mod pins: it lacks that release's rules and recipes, and the agent files are left as they are. `lidza update --cli-only --to %s` matches the app",
		d.cli, d.app, d.app)
}

// cliRelease is this CLI's release; tests set one.
var cliRelease = scaffold.Release

// warnDrift prints the drift, if any, once, up front.
func warnDrift(out io.Writer, dir string) *drift {
	d := frameworkDrift(dir)
	if d != nil {
		fmt.Fprintf(out, "[lidza] %s\n", d)
	}
	return d
}
