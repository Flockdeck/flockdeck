#!/bin/sh
# Install Flockdeck on macOS or Linux:
#
#   curl -fsSL https://flockdeck.ai/install.sh | sh
#
# It downloads the release archive for this machine from GitHub, checks it
# against the release's checksums.txt, and puts the one binary in ~/.local/bin.
# That is a directory the user owns, which matters later: the application
# updates itself in place, and it should never need a password to do it.
#
# Settings, all optional, read from the environment:
#
#   FLOCKDECK_VERSION      a release tag such as v0.1.1; the latest by default.
#                          To stay on it, also set FLOCKDECK_UPDATE=off where
#                          Flockdeck runs, or it updates itself to the latest.
#   FLOCKDECK_INSTALL_DIR  where the binary goes; ~/.local/bin by default
#   FLOCKDECK_DOWNLOAD     where release files are fetched from, for a mirror
#
# The archive names below are the ones cmd/release writes, and the updater in
# internal/selfupdate reads. The three have to agree.
#
# Everything is inside main, which is called on the last line, so a download
# cut short halfway through defines some functions and runs nothing.

set -eu

REPO="jmwri/flockdeck"

say() { printf 'flockdeck: %s\n' "$*"; }
die() { printf 'flockdeck: %s\n' "$*" >&2; exit 1; }

# fetch downloads a URL to a file with whichever of curl and wget is present.
fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 2 -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1"
	else
		die "downloading the release needs curl or wget"
	fi
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		die "checking the download needs sha256sum or shasum, and it is not installed unchecked"
	fi
}

detect_os() {
	case "$(uname -s)" in
		Linux) echo linux ;;
		Darwin) echo darwin ;;
		*) die "$(uname -s) is not covered by this script; on Windows, in PowerShell: irm https://flockdeck.ai/install.ps1 | iex" ;;
	esac
}

detect_arch() {
	arch=$(uname -m)
	# A shell running under Rosetta reports x86_64 on Apple silicon. The
	# native build is the one worth having.
	if [ "$1" = darwin ] && [ "$arch" = x86_64 ] &&
		[ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = 1 ]; then
		arch=arm64
	fi
	case "$arch" in
		x86_64 | amd64) echo amd64 ;;
		aarch64 | arm64) echo arm64 ;;
		*) die "no release is built for $arch" ;;
	esac
}

main() {
	os=$(detect_os)
	arch=$(detect_arch "$os")
	dir=${FLOCKDECK_INSTALL_DIR:-"$HOME/.local/bin"}
	base=${FLOCKDECK_DOWNLOAD:-"https://github.com/$REPO/releases/download"}

	tmp=$(mktemp -d 2>/dev/null || mktemp -d -t flockdeck)
	trap 'rm -rf "$tmp"' EXIT
	trap 'exit 1' INT TERM

	version=${FLOCKDECK_VERSION:-}
	if [ -z "$version" ]; then
		fetch "https://api.github.com/repos/$REPO/releases/latest" "$tmp/latest.json" ||
			die "could not find the latest release; set FLOCKDECK_VERSION to choose one"
		version=$(sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' "$tmp/latest.json" | head -n 1)
		[ -n "$version" ] || die "GitHub's answer did not name a release"
	fi
	# Releases are tagged v1.2.3, and a version is as often written without
	# the v; either finds the release rather than a download that is not there.
	case "$version" in v*) ;; *) version="v$version" ;; esac

	archive="flockdeck_${version}_${os}_${arch}.tar.gz"
	say "downloading $archive"
	fetch "$base/$version/$archive" "$tmp/$archive" ||
		die "could not download $base/$version/$archive"
	fetch "$base/$version/checksums.txt" "$tmp/checksums.txt" ||
		die "could not download the checksums for $version"

	want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
	[ -n "$want" ] || die "checksums.txt for $version does not list $archive"
	got=$(sha256 "$tmp/$archive")
	[ "$got" = "$want" ] || die "$archive does not match its published checksum; nothing was installed"

	tar -xzf "$tmp/$archive" -C "$tmp" flockdeck || die "could not unpack $archive"

	mkdir -p "$dir" || die "could not create $dir"
	# Written beside the destination and renamed over it: a copy that is
	# running keeps its old file, and nothing ever runs half a binary.
	cp "$tmp/flockdeck" "$dir/.flockdeck.new" &&
		chmod 755 "$dir/.flockdeck.new" &&
		mv -f "$dir/.flockdeck.new" "$dir/flockdeck" ||
		{ rm -f "$dir/.flockdeck.new"; die "could not write $dir/flockdeck"; }
	say "installed $version to $dir/flockdeck"
	if [ -n "${FLOCKDECK_VERSION:-}" ]; then
		say "to stay on $version, set FLOCKDECK_UPDATE=off where Flockdeck runs; otherwise it updates itself to the latest"
	fi

	case ":$PATH:" in
		*":$dir:"*) say "start it with: flockdeck" ;;
		*) say "$dir is not on your PATH; add this line to your shell's profile:"
			printf '    export PATH="%s:$PATH"\n' "$dir" ;;
	esac
	# A copy found first on PATH -- a go install, say -- is the one that runs.
	found=$(command -v flockdeck 2>/dev/null || true)
	if [ -n "$found" ] && [ "$found" != "$dir/flockdeck" ]; then
		say "note: the flockdeck your shell finds first is $found, not this one"
	fi
	# Starting it again while an older copy runs joins that copy instead.
	say "if Flockdeck is already running, quit it before starting this version"
}

main "$@"
