#!/bin/sh
# linux-package-smoke.sh installs a flockdeck .deb or .rpm in the clean
# container it runs in, checks that what nfpm.yaml promises is really on the
# machine, removes the package again, and checks nothing was left behind.
#
#   linux-package-smoke.sh deb|rpm PACKAGE EXPECTED_VERSION
#
# It is run as root, inside a bare distro image, by
# .github/workflows/linux-packages.yml. Nothing here is specific to CI: the
# same command inside `docker run` on a developer's machine gives the same
# answer.
set -eu

format="$1"
pkg="$2"
want_version="$3"

fail() {
	echo "::error::$*"
	exit 1
}

# Every file nfpm.yaml installs, and the mode each must have.
bin=/usr/bin/flockdeck
desktop=/usr/share/applications/flockdeck.desktop
icon=/usr/share/icons/hicolor/512x512/apps/flockdeck.png
docdir=/usr/share/doc/flockdeck
files="$bin $desktop $icon $docdir/LICENSE $docdir/THIRD-PARTY-NOTICES.md"

# Anything of ours left on the machine after the package is removed. The
# package file itself lives under a path with "flockdeck" in it, so it is
# excluded rather than found.
leftovers() {
	find / -xdev \( -path /proc -o -path /sys -o -path /dev -o -path /var/cache -o -path /var/lib/apt/lists -o -path /var/lib/dnf -o -path /var/log \) -prune -o \
		-name '*flockdeck*' -print 2>/dev/null | grep -v -F "$pkg" || true
}

# desktop-file-validate is the freedesktop.org reference validator. It is
# installed before the package, not after, so that postinstall.sh runs with
# update-desktop-database around, the case it exists for, as well as (in the
# bare image) without it.
echo "::group::Install desktop-file-utils and the tools the checks use"
case "$format" in
deb)
	export DEBIAN_FRONTEND=noninteractive
	# Docker's ubuntu and debian images tell dpkg to drop /usr/share/doc
	# (except copyright) to stay small, which would silently skip the
	# LICENSE this package installs. A real machine has no such rule.
	rm -f /etc/dpkg/dpkg.cfg.d/excludes
	apt-get update -qq
	apt-get install -y -qq --no-install-recommends desktop-file-utils file ca-certificates
	;;
rpm)
	dnf install -y -q desktop-file-utils file
	;;
*) fail "format must be deb or rpm, not $format" ;;
esac
echo "::endgroup::"

# Nothing of ours may be here before the install, or the removal check below
# could not tell a clean removal from a file that was never installed.
before=$(leftovers)
[ -z "$before" ] || fail "the image is not clean before the install: $before"

echo "::group::Install $pkg"
case "$format" in
deb)
	# apt, not dpkg -i: dpkg -i would stop at the first unmet dependency,
	# and the dependencies (libgtk-4-1, libwebkitgtk-6.0-4) being real
	# package names in this release of the distro is part of what is checked.
	apt-get install -y -qq --no-install-recommends "./$pkg"
	;;
rpm)
	# dnf, for the same reason: rpm -i would only report a dependency
	# (gtk4, webkitgtk6.0) it could not find.
	dnf install -y -q "./$pkg"
	;;
esac
echo "::endgroup::"

echo "Checking the installed files"
for f in $files; do
	[ -f "$f" ] || fail "$f was not installed"
done
[ -x "$bin" ] || fail "$bin is not executable"
for f in $desktop $icon $docdir/LICENSE $docdir/THIRD-PARTY-NOTICES.md; do
	mode=$(stat -c %a "$f")
	[ "$mode" = 644 ] || fail "$f is mode $mode, not 644"
done
mode=$(stat -c %a "$bin")
[ "$mode" = 755 ] || fail "$bin is mode $mode, not 755"

echo "Checking the package manager knows every file"
case "$format" in
deb) owned=$(dpkg -L flockdeck) ;;
rpm) owned=$(rpm -ql flockdeck) ;;
esac
for f in $files; do
	printf '%s\n' "$owned" | grep -qx "$f" || fail "$f is on disk but not in the package's file list"
done

# The binary is the real one, so the shared libraries it links against are
# the ones the package's own dependencies were meant to bring. A missing one
# is a dependency the spec forgot, or named wrongly for this distro.
echo "Checking the binary's shared libraries resolve"
if ldd "$bin" | grep -q 'not found'; then
	ldd "$bin"
	fail "$bin links against libraries this install did not bring: the package's dependencies are incomplete"
fi

# -version exits before any window is opened (main.go), so it needs no display.
echo "Running flockdeck -version"
got=$("$bin" -version) || fail "flockdeck -version failed: $got"
echo "$got"
case "$got" in
"flockdeck $want_version "*) ;;
*) fail "flockdeck -version said '$got', not version $want_version" ;;
esac

echo "Checking the desktop entry"
desktop-file-validate "$desktop" || fail "$desktop is not a valid desktop entry"
grep -qx 'Exec=flockdeck' "$desktop" || fail "$desktop does not run flockdeck"
grep -qx 'Icon=flockdeck' "$desktop" || fail "$desktop names no icon called flockdeck"
command -v flockdeck >/dev/null || fail "the Exec= name flockdeck is not on PATH"

echo "Checking the icon is a 512x512 PNG"
info=$(file -b "$icon")
case "$info" in
"PNG image data, 512 x 512"*) ;;
*) fail "$icon is '$info', not a 512x512 PNG" ;;
esac

echo "::group::Remove the package"
case "$format" in
deb) apt-get purge -y -qq flockdeck ;; # purge: remove alone keeps dpkg's own record of the package
rpm) rpm -e flockdeck ;;
esac
echo "::endgroup::"

echo "Checking the removal left nothing behind"
for f in $files; do
	[ ! -e "$f" ] || fail "$f is still there after the package was removed"
done
[ ! -e "$docdir" ] || fail "$docdir is still there after the package was removed"
case "$format" in
deb) dpkg -s flockdeck 2>/dev/null | grep -q '^Status: install ok installed' && fail "dpkg still lists flockdeck as installed" ;;
rpm) ! rpm -q flockdeck >/dev/null 2>&1 || fail "rpm still lists flockdeck as installed" ;;
esac
after=$(leftovers)
[ -z "$after" ] || fail "the removal left files behind: $after"

echo "OK: $pkg installs, works and removes cleanly"
