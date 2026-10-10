#!/bin/sh
# scripts/fuzz.sh [duration]: runs every fuzz target in the repository
# for the duration each (default 30s), stopping at the first failure.
# Their seed inputs already run with `go test`; this explores beyond
# them. A failing input is saved under the package's testdata/fuzz:
# commit it with the fix, so it stays a regression test.
set -eu
time=${1:-30s}
for pkg in $(go list ./... | sed 's|^github.com/agim/lidza|.|'); do
	for f in $(grep -ho '^func Fuzz[A-Za-z0-9_]*' "$pkg"/*_test.go 2>/dev/null | sed 's/^func //'); do
		echo "== $pkg $f"
		go test "$pkg" -run '^$' -fuzz "^$f\$" -fuzztime "$time" >/dev/null || {
			echo "fuzz failure: go test $pkg -run '^$f\$' shows it" >&2
			exit 1
		}
	done
done
echo "every fuzz target passed ($time each)"
