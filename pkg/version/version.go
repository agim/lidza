// Package version reports the Līdza build version.
package version

import "runtime/debug"

// Version is set at build time with
// -ldflags "-X github.com/agim/lidza/pkg/version.Version=v0.1.0".
// When unset, the module version from the Go build info is used, which is
// what `go install github.com/agim/lidza/cmd/lidza@latest` records.
var Version = ""

// String returns the version to show to users: the ldflags value, else the
// module version from build info, else "dev".
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
