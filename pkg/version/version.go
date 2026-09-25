// Package version reports the Līdza build version.
package version

import (
	"runtime/debug"
	"strings"
)

// Version is set at build time with
// -ldflags "-X github.com/agim/lidza/pkg/version.Version=v0.1.0".
// When unset, the module version from the Go build info is used, which is
// what `go install github.com/agim/lidza/cmd/lidza@latest` records.
var Version = ""

// String returns the version to show to users: the ldflags value, else the
// module version from build info, else "dev". A build from a checkout
// carries the version Go stamps from git: the tag when the commit is
// tagged, a pseudo-version with the commit otherwise, "+dirty" appended
// for uncommitted changes.
func String() string {
	if Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}

// Module returns the version of the framework module to fetch for an app
// created by this build (`go get github.com/agim/lidza@<Module>`): the
// tag or pseudo-version this CLI was built from, without a "+dirty"
// suffix, or "" when the build carries no version.
func Module() string {
	v := String()
	if !strings.HasPrefix(v, "v") {
		return ""
	}
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	return v
}
