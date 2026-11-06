#!/bin/sh
# Builds every first-party extension under extensions/ into a .rpx bundle in
# internal/extensions/builtin/dist/, where the panel binary embeds it.
# Built-in bundles are trusted because they ship inside the binary; set
# RIVET_EXT_SIGNING_KEY (a key file, or env:NAME) to sign them as well, for
# example to publish them for upload into other panels.
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
out="$root/internal/extensions/builtin/dist"
go=${GO:-go}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
find "$out" -mindepth 1 ! -name .gitkeep -delete
for dir in "$root"/extensions/*/; do
	name=$(basename "$dir")
	[ -f "$dir/manifest.json" ] || continue
	stage="$work/$name"
	mkdir -p "$stage"
	(cd "$root" && CGO_ENABLED=0 GOOS=wasip1 GOARCH=wasm "$go" build -buildmode=c-shared -trimpath -ldflags='-s -w' -o "$stage/plugin.wasm" "./extensions/$name")
	cp "$dir/manifest.json" "$stage/"
	[ -f "$dir/icon.svg" ] && cp "$dir/icon.svg" "$stage/"
	if [ -d "$dir/ui" ]; then
		mkdir -p "$stage/ui"
		cp "$dir"/ui/*.html "$stage/ui/"
	fi
	set -- -dir "$stage" -out "$out/$name.rpx"
	if [ -n "${RIVET_EXT_SIGNING_KEY:-}" ]; then
		set -- "$@" -key "$RIVET_EXT_SIGNING_KEY"
	fi
	(cd "$root" && "$go" run ./cmd/rivet-ext pack "$@")
done
