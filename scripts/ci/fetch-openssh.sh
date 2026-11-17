#!/bin/sh
# Copyright (c) 2026 Costas Kirgoussios
#
# This software is licensed under the PolyForm Noncommercial License 1.0.0.
# You may obtain a copy of the License at:
# https://polyformproject.org/licenses/noncommercial/1.0.0/

# Fetches the sshd sample (LogHub's OpenSSH_2k) from a pinned revision into
# testdata/fetched/, which git ignores: LogHub's terms do not allow the file in
# this repository. The download is data, never code: it is checked against a
# checksum fixed here and only then moved into place; nothing reads or runs it.
# A file that is already there with the right checksum is left alone.
set -eu

url=https://raw.githubusercontent.com/logpai/loghub/dd61d0952749ee7963bde24220d1be5ede023033/OpenSSH/OpenSSH_2k.log
want=1e4912727fa88245113d41b16a0cd25ceadba7f931e1c406542885b91254264f
dir=testdata/fetched
dest=$dir/OpenSSH_2k.log
part=$dest.part

# Work from the root of the repository, whatever the folder the script is run in.
cd "$(dirname "$0")/../.."

# The temporary file is removed on every way out; after a successful move it is gone already.
trap 'rm -f "$part"' EXIT
trap 'exit 1' HUP INT TERM

# Prints the SHA-256 of the file named by the first argument, in hexadecimal.
sum_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | { read -r s _; printf '%s\n' "$s"; }
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | { read -r s _; printf '%s\n' "$s"; }
	else
		echo "fetch-openssh: neither sha256sum nor shasum is on the path" >&2
		return 1
	fi
}

mkdir -p "$dir"

if [ -f "$dest" ]; then
	have=$(sum_of "$dest")
	if [ "$have" = "$want" ]; then
		echo "fetch-openssh: ok (already present)"
		exit 0
	fi
	echo "fetch-openssh: $dest has the checksum $have, not the recorded one; fetching it again"
fi

if ! command -v curl >/dev/null 2>&1; then
	echo "fetch-openssh: curl is not on the path" >&2
	exit 1
fi

rm -f "$part"
echo "fetch-openssh: downloading $url"
if ! curl -fsSL --retry 3 -o "$part" "$url"; then
	echo "fetch-openssh: the download failed" >&2
	exit 1
fi

got=$(sum_of "$part")
if [ "$got" != "$want" ]; then
	echo "fetch-openssh: the download has the checksum $got, want $want; it is discarded" >&2
	exit 1
fi

mv -f "$part" "$dest"
echo "fetch-openssh: ok"
