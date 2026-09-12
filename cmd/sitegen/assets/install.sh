#!/bin/sh
# Install Flockdeck on macOS or Linux:
#
#   curl -fsSL https://flockdeck.ai/install.sh | sh
#
# It downloads the release archive for this machine from dl.flockdeck.ai, or
# from GitHub, which carries every release too, when that cannot be reached;
# checks it against the release's checksums.txt from the same place; and puts
# the one binary in ~/.local/bin. That is a directory the user owns, which
# matters later: the application updates itself in place, and it should never
# need a password to do it.
#
# Settings, all optional, read from the environment:
#
#   FLOCKDECK_VERSION      a release tag such as v0.2.8; the latest by default.
#                          To stay on it, also set FLOCKDECK_UPDATE=off where
#                          Flockdeck runs, or it updates itself to the latest.
#   FLOCKDECK_INSTALL_DIR  where the binary goes; ~/.local/bin by default
#   FLOCKDECK_DOWNLOAD     a mirror to fetch the release files from instead,
#                          laid out as <mirror>/<version>/<file>; the latest is
#                          read from <mirror>/latest.json, or asked of GitHub
#
# The archive names below are the ones cmd/release writes, and the updater in
# internal/selfupdate reads. The three have to agree.
#
# Everything is inside main, which is called on the last line, so a download
# cut short halfway through defines some functions and runs nothing.

set -eu

REPO="jmwri/flockdeck"

# Where releases are found: dl.flockdeck.ai first, then GitHub. The tests
# point these at servers of their own.
DL="https://dl.flockdeck.ai"
GITHUB="https://github.com"
GITHUB_API="https://api.github.com"

say() { printf 'flockdeck: %s\n' "$*"; }
die() { printf 'flockdeck: %s\n' "$*" >&2; exit 1; }

# fetch downloads a URL to a file with whichever of curl and wget is present.
# A place that cannot be reached gives up in seconds rather than hanging, so
# that the next one is tried.
fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 2 --connect-timeout 15 -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -T 30 -O "$2" "$1"
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
		# Git Bash, MSYS2 and Cygwin: Windows, which has its own installer.
		MINGW* | MSYS* | CYGWIN*) die "on Windows, install from PowerShell instead: irm https://flockdeck.ai/install.ps1 | iex" ;;
		*) die "no release is built for $(uname -s); with Go installed, go install github.com/$REPO@latest builds it from source" ;;
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

# latest_from prints the version the latest.json at $1 names, which is all the
# file holds: {"version":"v1.2.3"}. There is no jq to lean on everywhere this
# runs, and none is needed, and anything that does not read as a release
# version is refused rather than downloaded.
latest_from() {
	fetch "$1/latest.json" "$tmp/latest.json" 2>/dev/null || return 1
	v=$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' "$tmp/latest.json" | head -n 1)
	case "$v" in
		v[0-9]*.[0-9]*.[0-9]*) echo "$v" ;;
		*) return 1 ;;
	esac
}

# latest_from_github prints the tag of the latest release on GitHub.
latest_from_github() {
	fetch "$GITHUB_API/repos/$REPO/releases/latest" "$tmp/release.json" || return 1
	sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' "$tmp/release.json" | head -n 1
}

# download fetches the archive and its checksums from one place, $1, so that
# the one is always checked against that place's own copy of the other.
download() {
	fetch "$1/$version/$archive" "$tmp/$archive" &&
		fetch "$1/$version/checksums.txt" "$tmp/checksums.txt"
}

main() {
	os=$(detect_os)
	arch=$(detect_arch "$os")
	# Run from a service or under env -i there may be no HOME, and set -u
	# would stop on it with nothing to say what to do instead.
	if [ -z "${FLOCKDECK_INSTALL_DIR:-}" ] && [ -z "${HOME:-}" ]; then
		die "HOME is not set; set FLOCKDECK_INSTALL_DIR to the directory to install into"
	fi
	dir=${FLOCKDECK_INSTALL_DIR:-"$HOME/.local/bin"}

	# A mirror given by hand is the only place files are fetched from.
	if [ -n "${FLOCKDECK_DOWNLOAD:-}" ]; then
		primary=${FLOCKDECK_DOWNLOAD%/}
		fallback=
	else
		primary=$DL
		fallback="$GITHUB/$REPO/releases/download"
	fi

	tmp=$(mktemp -d 2>/dev/null || mktemp -d -t flockdeck)
	trap 'rm -rf "$tmp"' EXIT
	trap 'exit 1' INT TERM

	version=${FLOCKDECK_VERSION:-}
	if [ -z "$version" ]; then
		if ! version=$(latest_from "$primary"); then
			if [ -n "$fallback" ]; then
				say "could not reach $primary; downloading from GitHub instead"
				primary=$fallback
				fallback=
			fi
			version=$(latest_from_github) ||
				die "could not find the latest release; set FLOCKDECK_VERSION to choose one"
			[ -n "$version" ] || die "GitHub's answer did not name a release"
		fi
	fi
	# Releases are tagged v1.2.3, and a version is as often written without
	# the v; either finds the release rather than a download that is not there.
	case "$version" in v*) ;; *) version="v$version" ;; esac

	archive="flockdeck_${version}_${os}_${arch}.tar.gz"
	say "downloading $archive"
	if [ -n "$fallback" ]; then
		# A release from before dl.flockdeck.ai is only on GitHub, as is
		# everything while the site cannot be reached.
		if ! download "$primary" 2>/dev/null; then
			say "could not download it from $primary; downloading it from GitHub instead"
			download "$fallback" || die "could not download $fallback/$version/$archive"
		fi
	else
		download "$primary" || die "could not download $primary/$version/$archive"
	fi

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

	# A copy found first on PATH -- a go install, say -- is the one that runs,
	# so it is not the one to be told to start.
	found=$(command -v flockdeck 2>/dev/null || true)
	shadowed=
	if [ -n "$found" ] && [ "$found" != "$dir/flockdeck" ]; then
		shadowed=1
	fi
	case ":$PATH:" in
		*":$dir:"*)
			if [ -n "$shadowed" ]; then
				say "start it with: $dir/flockdeck"
			else
				say "start it with: flockdeck"
			fi ;;
		*) say "$dir is not on your PATH; add this line to your shell's profile:"
			# Escaped for the double quotes it is printed in, so a directory with
			# a quote, a dollar or a backslash in its name still pastes as itself.
			printf '    export PATH="%s:$PATH"\n' "$(printf '%s' "$dir" | sed 's/[\\"$`]/\\&/g')" ;;
	esac
	if [ -n "$shadowed" ]; then
		say "note: the flockdeck your shell finds first is $found, not this one"
	fi
	# Starting it again while an older copy runs joins that copy instead.
	say "if Flockdeck is already running, quit it before starting this version"
}

main "$@"
