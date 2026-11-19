#!/bin/sh
# Renders the Homebrew formula for a release.
# Usage: scripts/render-formula.sh <version without v> <sha256 of the source tarball>
set -eu
[ $# -eq 2 ] || { echo "usage: $0 VERSION SHA256" >&2; exit 2; }
dir=$(cd "$(dirname "$0")/.." && pwd)
sed -e "s/@VERSION@/$1/g" -e "s/@SHA256@/$2/g" "$dir/packaging/homebrew/claude-syncer.rb.in"
