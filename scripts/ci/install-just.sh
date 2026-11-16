#!/bin/sh
# Copyright (c) 2026 Costas Kirgoussios
#
# This software is licensed under the PolyForm Noncommercial License 1.0.0.
# You may obtain a copy of the License at:
# https://polyformproject.org/licenses/noncommercial/1.0.0/

# Puts the pinned version of just on the path of a GitHub-hosted runner.
# The download is checked against a checksum fixed here.
set -eu

version=1.58.0
case "${RUNNER_OS:?}-${RUNNER_ARCH:?}" in
Linux-X64)
	asset="just-$version-x86_64-unknown-linux-musl.tar.gz"
	sum=4a5cc2f53e6f0f8c59092a6cc38291eb729d46a7dd95d3ae582008881b84931d
	;;
Windows-X64)
	asset="just-$version-x86_64-pc-windows-msvc.zip"
	sum=759f16fb7aa17c5c8b9594b6d4a8c1a6630dfd042cf2b3ff84841454d3d188dc
	;;
Windows-ARM64)
	asset="just-$version-aarch64-pc-windows-msvc.zip"
	sum=3a39ed629eb67678976c811a4da46f7985a2c22f4dbabe017b8b2eb5ceb5d01c
	;;
*)
	echo "install-just: no pinned build for $RUNNER_OS-$RUNNER_ARCH" >&2
	exit 1
	;;
esac

tmp="${RUNNER_TEMP:?}"
if command -v cygpath >/dev/null 2>&1; then
	work="$(cygpath -u "$tmp")/just-bin"
else
	work="$tmp/just-bin"
fi
mkdir -p "$work"

curl -fsSL -o "$work/$asset" "https://github.com/casey/just/releases/download/$version/$asset"
echo "$sum  $work/$asset" | sha256sum -c -

case "$asset" in
*.tar.gz) tar -xzf "$work/$asset" -C "$work" just ;;
*.zip)
	if command -v unzip >/dev/null 2>&1; then
		unzip -o -q "$work/$asset" just.exe -d "$work"
	else
		# Windows' own tar reads zip files; the tar that comes with Git does not.
		"$(cygpath -u "${SYSTEMROOT:?}")/System32/tar.exe" -xf "$(cygpath -w "$work/$asset")" -C "$(cygpath -w "$work")" just.exe
	fi
	;;
esac

if command -v cygpath >/dev/null 2>&1; then
	cygpath -w "$work" >>"${GITHUB_PATH:?}"
else
	echo "$work" >>"${GITHUB_PATH:?}"
fi
"$work/just" --version
