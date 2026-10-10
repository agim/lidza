#!/bin/sh
# Cut a release: scripts/release.sh vX.Y.Z
#
# Turns the "## Unreleased" section of CHANGELOG.md into the release
# heading, pins the installer to the release, commits, tags and pushes
# master and the tag together. CI's release job then installs the tag
# through Go and checks `lidza version`. Needs a clean tree on master with
# an "## Unreleased" section that has at least one entry.
#
# RELEASE_TAG=ci pushes master only and leaves the tag to the tag workflow
# (.github/workflows/tag.yml), for a place that may push master but not
# tags, such as an agent's cloud session.
set -eu
# govulncheck release run by the vulnerability check below.
GOVULNCHECK_VERSION=${GOVULNCHECK_VERSION:-v1.8.0}
ver=${1:-}
tagger=${RELEASE_TAG:-local}
case "$tagger" in local|ci) ;; *) echo "RELEASE_TAG is local or ci" >&2; exit 2 ;; esac
case "$ver" in v[0-9]*.[0-9]*.[0-9]*) ;; *) echo "usage: scripts/release.sh vX.Y.Z" >&2; exit 2 ;; esac
cd "$(dirname "$0")/.."
[ "$(git branch --show-current)" = master ] || { echo "not on master" >&2; exit 1; }
[ -z "$(git status --porcelain)" ] || { echo "working tree not clean" >&2; exit 1; }
git rev-parse -q --verify "refs/tags/$ver" >/dev/null && { echo "tag $ver exists" >&2; exit 1; }
grep -q '^## Unreleased$' CHANGELOG.md || { echo "CHANGELOG.md has no '## Unreleased' section" >&2; exit 1; }
sed -n '/^## Unreleased$/,/^## v/p' CHANGELOG.md | grep -q '^- ' || { echo "'## Unreleased' has no entries" >&2; exit 1; }
# CI's first check: a file gofmt would change fails the release run.
unformatted=$(gofmt -l $(git ls-files '*.go'))
[ -z "$unformatted" ] || { echo "gofmt: $unformatted" >&2; exit 1; }
# The module zip carries every tracked file to every app that fetches
# Līdza: nothing over 2 MB (a stray `go build` at the root was once).
big=$(git ls-files -z | xargs -0 -r ls -ln 2>/dev/null | awk '$5 > 2097152 {print $9}')
[ -z "$big" ] || { echo "tracked files over 2 MB: $big" >&2; exit 1; }
git pull -q --rebase=merges origin master
# What CI's framework job runs first: a release never tags a tree that
# fails it (v0.1.73 did, on an unused constant).
go vet ./... || { echo "go vet failed" >&2; exit 1; }
if command -v staticcheck >/dev/null; then
	staticcheck ./... || { echo "staticcheck failed" >&2; exit 1; }
else
	echo "staticcheck not installed: CI will run it (go install honnef.co/go/tools/cmd/staticcheck@latest)" >&2
fi
# Known vulnerabilities in code Līdza calls (Go's vulnerability
# database; a module that has one but whose vulnerable code is never
# called does not fail it). Offline, it warns and CI runs it.
if ! out=$(go run golang.org/x/vuln/cmd/govulncheck@$GOVULNCHECK_VERSION ./... 2>&1); then
	case "$out" in
	*"affected by"*) echo "$out" >&2; echo "govulncheck failed: upgrade the module it names" >&2; exit 1 ;;
	*) echo "govulncheck could not run (offline?); CI runs it: $out" | tail -3 >&2 ;;
	esac
fi
today=$(date -u +%Y-%m-%d)
sed -i.bak "s/^## Unreleased$/## $ver ($today)/" CHANGELOG.md && rm CHANGELOG.md.bak
sed -i.bak "s/^LIDZA_VERSION=\"\${LIDZA_VERSION:-v[0-9.]*}\"$/LIDZA_VERSION=\"\${LIDZA_VERSION:-$ver}\"/" install.sh && rm install.sh.bak
go test ./pkg/version >/dev/null
git add CHANGELOG.md install.sh
git commit -q -m "Release $ver"
if [ "$tagger" = ci ]; then
	git push -q -u origin master
	commit=$(git rev-parse --short=12 HEAD)
	echo "pushed $ver: $commit; the tag workflow tags it (a session with tag rights can too: git tag -a $ver $commit -m $ver && git push origin $ver)."
	echo "Until the tag exists, apps take it by commit: lidza update --to $commit"
	exit 0
fi
git tag -a "$ver" -m "$ver"
# One push for both, so the tag workflow finds the tag already there.
git push -q --atomic -u origin master "$ver"
echo "released $ver: $(git rev-parse --short HEAD); go install github.com/agim/lidza/cmd/lidza@$ver"
