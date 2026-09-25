package version

import (
	"os"
	"regexp"
	"testing"
)

// The installer pins the CLI release it ships with; the changelog names
// the newest release. They must agree, or a fresh machine installs a CLI
// that lacks what the docs describe. scripts/release.sh bumps both.
func TestInstallerPinMatchesChangelog(t *testing.T) {
	installer, err := os.ReadFile("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	changelog, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	pin := regexp.MustCompile(`(?m)^LIDZA_VERSION="\$\{LIDZA_VERSION:-(v[0-9]+\.[0-9]+\.[0-9]+)\}"$`).FindSubmatch(installer)
	if pin == nil {
		t.Fatal("install.sh has no LIDZA_VERSION pin")
	}
	newest := regexp.MustCompile(`(?m)^## (v[0-9]+\.[0-9]+\.[0-9]+) \(`).FindSubmatch(changelog)
	if newest == nil {
		t.Fatal("CHANGELOG.md has no release heading")
	}
	if string(pin[1]) != string(newest[1]) {
		t.Errorf("install.sh pins %s, CHANGELOG.md's newest release is %s: run scripts/release.sh", pin[1], newest[1])
	}
}
