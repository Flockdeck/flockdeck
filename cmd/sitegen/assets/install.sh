#!/bin/sh
# Install Flockdeck on macOS or Linux:
#
#   curl -fsSL https://flockdeck.ai/install.sh | sh
#
# It downloads the release archive for this machine from dl.flockdeck.ai, or
# from GitHub, which carries every release too, when that cannot be reached;
# checks it against the SHA-256 written into this script below; and puts the
# one binary in ~/.local/bin. That is a directory the user owns, which matters
# later: the application updates itself in place, and it should never need a
# password to do it.
#
# Settings, all optional, read from the environment:
#
#   FLOCKDECK_VERSION      a release tag such as v0.2.8; by default the release
#                          named in RELEASE below. Any other release is checked
#                          only against the checksums.txt downloaded beside it.
#                          To stay on it, also set FLOCKDECK_UPDATE=off where
#                          Flockdeck runs, or it updates itself to the latest.
#   FLOCKDECK_INSTALL_DIR  where the binary goes; ~/.local/bin by default
#   FLOCKDECK_DOWNLOAD     a mirror to fetch the release files from instead,
#                          laid out as <mirror>/<version>/<file>
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

# The release this script was published with, and the SHA-256 of each of its
# archives: cmd/sitegen writes both in from that release's checksums.txt when
# it generates the site. The archive is checked against these rather than
# against a checksums.txt fetched from where the archive was, because whoever
# could replace the archive there could replace that checksums.txt with it.
# These came with the script, from flockdeck.ai, over the connection it is
# already trusted over.
RELEASE="@RELEASE@"
RELEASE_SUMS="@RELEASE_SUMS@"

say() { printf 'flockdeck: %s\n' "$*"; }
die() { printf 'flockdeck: %s\n' "$*" >&2; exit 1; }

# shell_word writes a path so that it pastes into a shell as one word: as it
# is when it holds only what a plain path is made of, and otherwise in double
# quotes, with what a shell still reads inside those escaped.
shell_word() {
	case $1 in
		*[!A-Za-z0-9_./-]*) printf '"%s"' "$(printf '%s' "$1" | sed 's/[\\"$`]/\\&/g')" ;;
		*) printf '%s' "$1" ;;
	esac
}

# trim_slashes writes a path without the slashes on its end, which name the
# same directory it names without them; / is left as it is.
trim_slashes() {
	p=$1
	while [ "$p" != / ] && [ "${p%/}" != "$p" ]; do p=${p%/}; done
	printf '%s' "$p"
}

# on_path reports whether the directory $1, given without slashes on its end,
# is one of those on PATH, which may be written with them.
on_path() {
	# Split on the colons alone, and with nothing in an entry read as a
	# pattern to expand.
	set -f
	old_ifs=$IFS
	IFS=:
	on=1
	for entry in $PATH; do
		if [ "$(trim_slashes "$entry")" = "$1" ]; then on=0; fi
	done
	IFS=$old_ifs
	set +f
	return $on
}

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

# download fetches the archive from one place, $1. A release other than the
# one this script carries the checksums of needs its checksums.txt too, and
# takes it from the same place, so that the one is at least checked against
# that place's own copy of the other.
download() {
	fetch "$1/$version/$archive" "$tmp/$archive" || return 1
	[ -n "$pinned" ] || fetch "$1/$version/checksums.txt" "$tmp/checksums.txt"
}

main() {
	os=$(detect_os)
	arch=$(detect_arch "$os")
	# Asked once, here, rather than by the first fetch: that one's complaint
	# is thrown away with the rest of what an unreachable site says, and the
	# reader was told the site could not be reached, and that GitHub was being
	# tried instead, before hearing what was actually missing.
	if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
		die "downloading the release needs curl or wget, and neither is installed; install one and run this again"
	fi
	# Run from a service or under env -i there may be no HOME, and set -u
	# would stop on it with nothing to say what to do instead.
	if [ -z "${FLOCKDECK_INSTALL_DIR:-}" ] && [ -z "${HOME:-}" ]; then
		die "HOME is not set; set FLOCKDECK_INSTALL_DIR to the directory to install into"
	fi
	# A slash on the end names the same directory, and compared as typed it
	# did not: /usr/local/bin/ was "not on your PATH" while it was, and the
	# flockdeck just put there was "not this one". A HOME ending in one made
	# the default ~//.local/bin, which did the same.
	if [ -n "${FLOCKDECK_INSTALL_DIR:-}" ]; then
		dir=$(trim_slashes "$FLOCKDECK_INSTALL_DIR")
	else
		dir="$(trim_slashes "$HOME")"
		dir="${dir%/}/.local/bin"
	fi

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

	version=${FLOCKDECK_VERSION:-$RELEASE}
	# Releases are tagged v1.2.3, and a version is as often written without
	# the v; either finds the release rather than a download that is not there.
	case "$version" in v*) ;; *) version="v$version" ;; esac
	pinned=
	if [ "$version" = "$RELEASE" ]; then pinned=1; fi

	archive="flockdeck_${version}_${os}_${arch}.tar.gz"
	say "downloading $archive"
	if [ -z "$pinned" ]; then
		say "$version is not the release this script was published with ($RELEASE), so it is checked only against the checksums.txt downloaded beside it"
	fi
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

	if [ -n "$pinned" ]; then
		want=$(printf '%s\n' "$RELEASE_SUMS" | awk -v f="$archive" '$2 == f { print $1 }')
		[ -n "$want" ] || die "this script carries no checksum for $archive"
	else
		want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
		[ -n "$want" ] || die "checksums.txt for $version does not list $archive"
	fi
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
	# What PATH names is compared without the slashes on its end, as $dir is:
	# /opt/bin/ on it is where /opt/bin is, and the shell finds the copy there
	# as /opt/bin//flockdeck.
	found=$(command -v flockdeck 2>/dev/null || true)
	shadowed=
	if [ -n "$found" ] && [ "$(trim_slashes "${found%/*}")" != "$dir" ]; then
		shadowed=1
	fi
	if on_path "$dir"; then
		if [ -n "$shadowed" ]; then
			# Quoted when it has to be: a directory with a space in it
			# pasted as two words.
			say "start it with: $(shell_word "$dir/flockdeck")"
		else
			say "start it with: flockdeck"
		fi
	else
		say "$dir is not on your PATH; add this line to your shell's profile:"
		# Escaped for the double quotes it is printed in, so a directory with
		# a quote, a dollar or a backslash in its name still pastes as itself.
		printf '    export PATH="%s:$PATH"\n' "$(printf '%s' "$dir" | sed 's/[\\"$`]/\\&/g')"
	fi
	if [ -n "$shadowed" ]; then
		say "note: the flockdeck your shell finds first is $found, not this one"
	fi
	# Starting it again while an older copy runs joins that copy instead.
	say "if Flockdeck is already running, quit it before starting this version"
}

main "$@"
