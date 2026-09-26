#!/bin/sh
# Vendors the admin pages' front end into this directory: Tabler's
# stylesheet and script (https://tabler.io, MIT), gzipped, and the
# Tabler icons named in icons.txt (https://tabler.io/icons, MIT). The
# pages serve these from the binary, so they need no CDN and work under
# a strict Content-Security-Policy. Run after changing a version below
# or icons.txt:
#
#   sh packs/admin/assets/vendor.sh
set -eu
CORE=1.6.0
ICONS=3.48.0
cd "$(dirname "$0")"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "https://registry.npmjs.org/@tabler/core/-/core-$CORE.tgz" | tar xz -C "$tmp"
mkdir "$tmp/icons"
curl -fsSL "https://registry.npmjs.org/@tabler/icons/-/icons-$ICONS.tgz" | tar xz -C "$tmp/icons"
for f in css/tabler.min.css js/tabler.min.js; do
	# The source maps are not shipped; drop the comment that asks for one.
	sed 's|/\*# sourceMappingURL=[^*]*\*/||; s|//# sourceMappingURL=.*$||' "$tmp/package/dist/$f" | gzip -9n >"$(basename "$f").gz"
done
rm -rf icons
mkdir icons
while read -r name; do
	[ -n "$name" ] || continue
	cp "$tmp/icons/package/icons/outline/$name.svg" "icons/$name.svg"
done <icons.txt
printf 'Tabler core %s and Tabler icons %s, both MIT (https://github.com/tabler/tabler, https://github.com/tabler/tabler-icons).\n' "$CORE" "$ICONS" >VERSIONS
echo "vendored Tabler $CORE and $(wc -l <icons.txt | tr -d ' ') icons from Tabler icons $ICONS"
