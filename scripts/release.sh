#!/bin/sh
# Cut a release: scripts/release.sh vX.Y.Z
#
# Turns the "## Unreleased" section of CHANGELOG.md into the release
# heading, pins the installer to the release, commits, tags and pushes
# master and the tag. CI's release job then installs the tag through Go
# and checks `lidza version`. Needs a clean tree on master with an
# "## Unreleased" section that has at least one entry.
set -eu
ver=${1:-}
case "$ver" in v[0-9]*.[0-9]*.[0-9]*) ;; *) echo "usage: scripts/release.sh vX.Y.Z" >&2; exit 2 ;; esac
cd "$(dirname "$0")/.."
[ "$(git branch --show-current)" = master ] || { echo "not on master" >&2; exit 1; }
[ -z "$(git status --porcelain)" ] || { echo "working tree not clean" >&2; exit 1; }
git rev-parse -q --verify "refs/tags/$ver" >/dev/null && { echo "tag $ver exists" >&2; exit 1; }
grep -q '^## Unreleased$' CHANGELOG.md || { echo "CHANGELOG.md has no '## Unreleased' section" >&2; exit 1; }
sed -n '/^## Unreleased$/,/^## v/p' CHANGELOG.md | grep -q '^- ' || { echo "'## Unreleased' has no entries" >&2; exit 1; }
git pull -q --rebase origin master
today=$(date -u +%Y-%m-%d)
sed -i.bak "s/^## Unreleased$/## $ver ($today)/" CHANGELOG.md && rm CHANGELOG.md.bak
sed -i.bak "s/^LIDZA_VERSION=\"\${LIDZA_VERSION:-v[0-9.]*}\"$/LIDZA_VERSION=\"\${LIDZA_VERSION:-$ver}\"/" install.sh && rm install.sh.bak
go test ./pkg/version >/dev/null
git add CHANGELOG.md install.sh
git commit -q -m "Release $ver"
git tag -a "$ver" -m "$ver"
git push -q -u origin master
git push -q origin "$ver"
echo "released $ver: $(git rev-parse --short HEAD); go install github.com/agim/lidza/cmd/lidza@$ver"
