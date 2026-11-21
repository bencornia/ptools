#!/bin/sh
# Installs ptools, from its latest release, and then runs plsp doctor --fix
# to install the P compiler and set up your editors:
#
#     curl -fsSL https://raw.githubusercontent.com/bencornia/ptools/main/install.sh | sh
#
# Set PTOOLS_BIN to install somewhere other than ~/.local/bin,
# PTOOLS_VERSION to install a release other than the latest, as in 0.8.0,
# and PTOOLS_FIX=0 to have the doctor only check, not fix.
set -eu

repo=https://github.com/bencornia/ptools
bin=${PTOOLS_BIN:-$HOME/.local/bin}
if [ -n "${PTOOLS_VERSION:-}" ]; then
	base=$repo/releases/download/v${PTOOLS_VERSION#v}
else
	base=$repo/releases/latest/download
fi
base=${PTOOLS_DOWNLOAD:-$base} # for trying a build before it's released

fail() {
	echo "install.sh: $*" >&2
	exit 1
}

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) fail "there's no build for $(uname -s); on Windows, download ptools_windows_amd64.zip from $repo/releases" ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) fail "there's no build for $(uname -m); build from source with: go install github.com/bencornia/ptools/cmd/...@latest" ;;
esac
archive=ptools_${os}_${arch}.tar.gz

if command -v curl > /dev/null; then
	fetch() { curl -fsSL -o "$2" "$1"; }
elif command -v wget > /dev/null; then
	fetch() { wget -q -O "$2" "$1"; }
else
	fail "downloading needs curl or wget"
fi
if command -v sha256sum > /dev/null; then
	sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum > /dev/null; then
	sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
else
	fail "checking the download needs sha256sum or shasum"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "Downloading $base/$archive"
fetch "$base/$archive" "$tmp/$archive" || fail "couldn't download $base/$archive"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || fail "couldn't download $base/checksums.txt"
want=$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || fail "checksums.txt has no checksum for $archive"
[ "$(sha256 "$tmp/$archive")" = "$want" ] || fail "$archive doesn't match its checksum"

mkdir -p "$tmp/ptools" "$bin"
tar -xzf "$tmp/$archive" -C "$tmp/ptools"
for tool in plsp prdb pfmt pdoc; do
	[ -f "$tmp/ptools/$tool" ] || fail "$archive has no $tool"
	# Replace rather than overwrite, which fails while the tool is running.
	cp "$tmp/ptools/$tool" "$bin/.$tool.new"
	chmod 755 "$bin/.$tool.new"
	mv -f "$bin/.$tool.new" "$bin/$tool"
done
echo "Installed plsp, prdb, pfmt and pdoc $("$bin/plsp" --version) in $bin"

if [ "${PTOOLS_FIX:-1}" = 0 ]; then
	exec "$bin/plsp" doctor
fi
exec "$bin/plsp" doctor --fix
