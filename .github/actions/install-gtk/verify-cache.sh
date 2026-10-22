#!/usr/bin/env bash
# verify-cache.sh DIR removes from DIR every .deb that is not exactly the file
# the package lists describe, and everything in it that is not a .deb. It exits
# non-zero if anything it meant to remove is still there.
#
# apt does not do this. When a file named for a package sits in the archive
# folder and its size is the one in the index, apt takes it as downloaded and
# does not look at its hash (apt-pkg/acquire-item.cc, pkgAcqArchive), and dpkg
# checks nothing either. A same-size file put in a restored cache would be
# installed as it is, its maintainer scripts run as root. So each file is read
# for its package, version and architecture, and kept only if its SHA-256 is one
# the signed index lists for that version, and its name is the one apt gives a
# file of that package. A file the index knows no such version of is removed
# too: it would not be used, and would only sit in the cache.
#
# The control fields come from the file being checked, so they are checked for
# their form before they reach apt-cache, which would take a name that starts
# with -o for an option and could be pointed at index files the cache holds.
#
# It must run after apt-get update, which checks the signature of the index.
set -uo pipefail

dir="$1"
removed=0
failed=0
# SUDO is empty when a test runs this as a user that owns the folder.
SUDO="${SUDO-sudo}"

remove() {
	echo "removing $1 from the cache: $2"
	if $SUDO rm -rf -- "$1" && [ ! -e "$1" ] && [ ! -L "$1" ]; then
		removed=$((removed + 1))
	else
		echo "::error::could not remove $1"
		failed=$((failed + 1))
	fi
}

# partial and lock are apt's own, and kept only as a real folder and a real
# file; a link in their place would be followed by what the action does to them.
while IFS= read -r -d '' f; do
	remove "$f" "not what apt keeps there"
done < <(find "$dir" -mindepth 1 -maxdepth 1 \( \( -name partial ! -type d \) -o \( -name lock ! -type f \) \) -print0)

# Anything that is not a .deb, a link or a folder included, has no use in an
# archive folder.
while IFS= read -r -d '' f; do
	remove "$f" "not a package file"
done < <(find "$dir" -mindepth 1 -maxdepth 1 ! -name '*.deb' ! -name partial ! -name lock -print0)
while IFS= read -r -d '' f; do
	remove "$f" "not a regular file"
done < <(find "$dir" -mindepth 1 -maxdepth 1 -name '*.deb' ! -type f -print0)

# listed SPEC HASH: the index lists HASH for the package version SPEC.
listed() {
	apt-cache show -- "$1" 2>/dev/null | awk '/^SHA256:/ {print $2}' | grep -qxF -- "$2"
}

for deb in "$dir"/*.deb; do
	[ -f "$deb" ] || continue
	name=$(dpkg-deb -f "$deb" Package 2>/dev/null)
	version=$(dpkg-deb -f "$deb" Version 2>/dev/null)
	arch=$(dpkg-deb -f "$deb" Architecture 2>/dev/null)
	if [ -z "$name" ] || [ -z "$version" ] || [ -z "$arch" ]; then
		remove "$deb" "no readable control data"
		continue
	fi
	if ! [[ $name =~ ^[a-z0-9][a-z0-9+.-]+$ ]] || ! [[ $arch =~ ^[a-z0-9-]+$ ]] || ! [[ $version =~ ^[0-9A-Za-z.+~:-]+$ ]]; then
		remove "$deb" "package, version or architecture is not of a valid form"
		continue
	fi
	# apt names a downloaded file name_version_arch.deb, a colon in the version
	# written as %3a.
	if [ "$(basename "$deb")" != "${name}_${version//:/%3a}_${arch}.deb" ]; then
		remove "$deb" "the file name is not the one for $name $version $arch"
		continue
	fi
	have=$(sha256sum -- "$deb" | awk '{print $1}')
	# The index lists a package of any architecture as name:arch=version, or
	# for one that is the same on all as name=version.
	if listed "$name:$arch=$version" "$have" || listed "$name=$version" "$have"; then
		continue
	fi
	remove "$deb" "$name $version $arch is not what the package lists describe"
done

echo "cached package files checked, $removed removed, $failed could not be removed"
[ "$failed" -eq 0 ]
