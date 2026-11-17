#!/bin/sh
# Copyright (c) 2026 Costas Kirgoussios
#
# This software is licensed under the PolyForm Noncommercial License 1.0.0.
# You may obtain a copy of the License at:
# https://polyformproject.org/licenses/noncommercial/1.0.0/

# Fetches the whole sshd log (LogHub's OpenSSH, 655,146 lines) from Zenodo into
# testdata/fetched/SSH_full.log, which git ignores. LogHub's terms do not allow
# the file in this repository; Zenodo's record states CC BY 4.0 for the data,
# and it is still never committed. The download is data, never code: it is
# checked against checksums fixed here and only then moved into place; nothing
# reads or runs it. A file that is already there with the right checksum is
# left alone.
#
# The values were measured once, on 2026-11-17, by downloading the file:
#   archive  SSH.tar.gz  4,596,666 bytes
#            SHA-256 296610a35773f58b7c295e5407fc5ccdf48beb3c544fc14a6309addf904a2712
#            MD5     a44e40b4348697dabf2bba56885e0a38 (what Zenodo's record states;
#                    this script checks the SHA-256 only, the MD5 is the cross-check
#                    of that figure against Zenodo)
#   member   SSH.log     73,417,506 bytes, 655,146 lines, the only member
#            SHA-256 89eee9e36c182722f9fadc6e5617a4818ffc66109e7e222b8f9e8b78fc3aa2f3
#
# Only that one member is extracted, into a fresh temporary folder next to the
# destination (so the final move is a rename); the folder is removed on every
# way out.
set -eu

url=https://zenodo.org/api/records/8196385/files/SSH.tar.gz/content
want_archive=296610a35773f58b7c295e5407fc5ccdf48beb3c544fc14a6309addf904a2712
want_log=89eee9e36c182722f9fadc6e5617a4818ffc66109e7e222b8f9e8b78fc3aa2f3
member=SSH.log
dir=testdata/fetched
dest=$dir/SSH_full.log

# Work from the root of the repository, whatever the folder the script is run in.
cd "$(dirname "$0")/../.."

# The temporary folder (the archive and the extracted member) is removed on
# every way out; after a successful move only the destination is left.
work=
trap 'if [ -n "$work" ]; then rm -rf "$work"; fi' EXIT
trap 'exit 1' HUP INT TERM

# Prints the SHA-256 of the file named by the first argument, in hexadecimal.
sum_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | { read -r s _; printf '%s\n' "$s"; }
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | { read -r s _; printf '%s\n' "$s"; }
	else
		echo "fetch-openssh-full: neither sha256sum nor shasum is on the path" >&2
		return 1
	fi
}

mkdir -p "$dir"

if [ -f "$dest" ]; then
	have=$(sum_of "$dest")
	if [ "$have" = "$want_log" ]; then
		echo "fetch-openssh-full: ok (already present)"
		exit 0
	fi
	echo "fetch-openssh-full: $dest has the checksum $have, not the recorded one; fetching it again"
fi

for tool in curl tar; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "fetch-openssh-full: $tool is not on the path" >&2
		exit 1
	fi
done

work=$(mktemp -d "$dir/.fetch-full.XXXXXX")
archive=$work/SSH.tar.gz
mkdir "$work/out"

echo "fetch-openssh-full: downloading $url (about 4.4 MiB)"
if ! curl -fsSL --proto '=https' --proto-redir '=https' --retry 3 -o "$archive" "$url"; then
	echo "fetch-openssh-full: the download failed" >&2
	exit 1
fi

got=$(sum_of "$archive")
if [ "$got" != "$want_archive" ]; then
	echo "fetch-openssh-full: the download has the checksum $got, want $want_archive; it is discarded" >&2
	exit 1
fi

# The archive must hold exactly one member, named $member: no other name, no
# folder, no "..". Anything else is refused before anything is extracted.
if ! members=$(tar -tzf "$archive"); then
	echo "fetch-openssh-full: the archive cannot be listed; it is discarded" >&2
	exit 1
fi
if [ "$members" != "$member" ]; then
	echo "fetch-openssh-full: the archive does not hold exactly the one member $member; it is discarded" >&2
	exit 1
fi

if ! tar -xzf "$archive" -C "$work/out" "$member"; then
	echo "fetch-openssh-full: extracting $member failed" >&2
	exit 1
fi
extracted=$work/out/$member
if [ ! -f "$extracted" ] || [ -L "$extracted" ]; then
	echo "fetch-openssh-full: $member is not a regular file; it is discarded" >&2
	exit 1
fi

got=$(sum_of "$extracted")
if [ "$got" != "$want_log" ]; then
	echo "fetch-openssh-full: $member has the checksum $got, want $want_log; it is discarded" >&2
	exit 1
fi

# The member keeps the mode and the time that the archive gave it; it is data.
chmod 644 "$extracted"
mv -f "$extracted" "$dest"
echo "fetch-openssh-full: ok"
