#!/usr/bin/env bash
# verify-cache.sh DIR removes from DIR every .deb that is not exactly the file
# the package lists describe, and everything in it that is not a .deb.
#
# apt does not do this. When a file named for a package sits in the archive
# folder and its size is the one in the index, apt takes it as downloaded and
# does not look at its hash (apt-pkg/acquire-item.cc, pkgAcqArchive), and dpkg
# checks nothing either. A same-size file put in a restored cache would be
# installed as it is, its maintainer scripts run as root. So each file is read
# for its package, version and architecture, and kept only if its SHA-256 is one
# the signed index lists for that version. A file the index knows no such
# version of is removed too: it would not be used, and would only sit in the
# cache.
#
# It must run after apt-get update, which checks the signature of the index.
set -uo pipefail

dir="$1"
removed=0

remove() {
	echo "removing $1 from the cache: $2"
	sudo rm -f -- "$1"
	removed=$((removed + 1))
}

# Anything that is not a .deb, a link included, has no use in an archive folder.
while IFS= read -r -d '' f; do
	remove "$f" "not a package file"
done < <(find "$dir" -mindepth 1 -maxdepth 1 ! -name '*.deb' ! -name partial ! -name lock -print0)
while IFS= read -r -d '' f; do
	remove "$f" "not a regular file"
done < <(find "$dir" -mindepth 1 -maxdepth 1 -name '*.deb' ! -type f -print0)

for deb in "$dir"/*.deb; do
	[ -f "$deb" ] || continue
	name=$(dpkg-deb -f "$deb" Package 2>/dev/null)
	version=$(dpkg-deb -f "$deb" Version 2>/dev/null)
	arch=$(dpkg-deb -f "$deb" Architecture 2>/dev/null)
	if [ -z "$name" ] || [ -z "$version" ]; then
		remove "$deb" "no readable control data"
		continue
	fi
	have=$(sha256sum -- "$deb" | awk '{print $1}')
	# The index lists a package of any architecture as name:arch=version, or
	# for one that is the same on all as name=version.
	if apt-cache show "$name:$arch=$version" 2>/dev/null | awk '/^SHA256:/ {print $2}' | grep -qx -- "$have"; then
		continue
	fi
	if apt-cache show "$name=$version" 2>/dev/null | awk '/^SHA256:/ {print $2}' | grep -qx -- "$have"; then
		continue
	fi
	remove "$deb" "$name $version $arch is not what the package lists describe"
done

echo "cached package files checked, $removed removed"
