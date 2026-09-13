#!/bin/sh
# Publish a release to dl.flockdeck.ai, the DigitalOcean Space behind
# DigitalOcean's CDN that the updater, the install scripts and the site
# download from first. GitHub carries every release as well; the release
# workflow publishes it there before this runs.
#
#   scripts/publish-downloads.sh v1.4.0 [dist]
#
# dist is what `go run ./cmd/release -version v1.4.0 -out dist` built and
# `go run ./cmd/release -sign -version v1.4.0 -out dist` signed. It needs only
# the aws CLI and curl, and any S3-compatible store will do, so it runs by hand
# as well as in the workflow: against a local MinIO to try it, or to finish a
# release whose workflow stopped part way.
#
# The order is the point of it. A release's files go up first, under its
# version, a path nothing refers to yet: the archives, checksums.txt and its
# signature, and the signed manifest.json the updater reads. One is read back
# through the public address. Then /latest/, for the site's download buttons,
# and last latest.json, which names the latest version and is the one file
# every updater asks for first, so that a release switches over in one step
# and never names files that are not there yet. A pre-release, such as
# v1.4.0-rc.1, goes up under its version and moves nothing: it is never the
# latest, here or on GitHub.
#
# Nothing is purged from the CDN, so this needs no DigitalOcean API token.
# What is under a version never changes, and is cached for a year. latest.json
# and /latest/ are cached for a minute, so for that long an edge or a browser
# may go on naming the release before. That holds an update back and does no harm:
# latest.json is not signed, and needs neither a signature nor a purge,
# because all it does is name a version, whose own signed manifest the
# updater checks before believing any of it.
#
# Read from the environment:
#
#   DO_SPACES_KEY, DO_SPACES_SECRET  a key that can write the bucket
#   DO_SPACES_BUCKET                 the bucket
#   DO_SPACES_REGION                 its region, such as lon1
#   DO_SPACES_ENDPOINT               the S3 endpoint; by default
#                                    https://$DO_SPACES_REGION.digitaloceanspaces.com
#   FLOCKDECK_DL_URL                 where the bucket is served to everyone;
#                                    https://dl.flockdeck.ai by default
#   FLOCKDECK_CHECK_WAIT             seconds between tries at reading a file
#                                    back through the public address; 5

set -eu

say() { printf 'publish-downloads: %s\n' "$*"; }
die() { printf 'publish-downloads: %s\n' "$*" >&2; exit 1; }

[ $# -ge 1 ] && [ $# -le 2 ] || die "usage: $0 <version> [dist]"
version=$1
dist=${2:-dist}

case "$version" in
	v[0-9]*.[0-9]*.[0-9]*) ;;
	*) die "$version is not a release version such as v1.4.0" ;;
esac
pre=
case "$version" in *-*) pre=1 ;; esac

# Everything missing is named at once, so one run says all there is to set.
missing=
for name in DO_SPACES_KEY DO_SPACES_SECRET DO_SPACES_BUCKET DO_SPACES_REGION; do
	[ -n "$(printenv "$name" || true)" ] || missing="$missing $name"
done
[ -z "$missing" ] || die "not set:$missing"
for tool in aws curl; do
	command -v "$tool" >/dev/null 2>&1 || die "publishing needs $tool, which is not installed"
done

# sha256 hashes a file read from its standard input, since sha256sum escapes
# a name with a backslash in it, as a Windows path has, and marks the hash.
sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum <"$1" | cut -d' ' -f1
	else
		shasum -a 256 <"$1" | cut -d' ' -f1
	fi
}

# What is about to be published is checked against its own checksums.txt
# first: the archives, once uploaded, are cached for a year.
set -- "$dist"/flockdeck_"$version"_*
[ -e "$1" ] || die "$dist holds no archives of $version; build them with: go run ./cmd/release -version $version -out $dist"
for f in checksums.txt checksums.txt.sig manifest.json manifest.json.sig latest.json; do
	[ -f "$dist/$f" ] || die "$dist/$f is missing; sign the release with: go run ./cmd/release -sign -version $version -out $dist"
done
for f in "$@"; do
	name=${f##*/}
	want=$(awk -v f="$name" '$2 == f { print $1 }' "$dist/checksums.txt")
	[ -n "$want" ] || die "checksums.txt does not list $name"
	[ "$(sha256 "$f")" = "$want" ] || die "$name does not match checksums.txt; build and sign the release again"
done
grep -qF "\"version\": \"$version\"" "$dist/manifest.json" ||
	die "$dist/manifest.json is not for $version; sign the release again"
grep -qF "\"version\":\"$version\"" "$dist/latest.json" ||
	die "$dist/latest.json does not name $version; sign the release again"

bucket=$DO_SPACES_BUCKET
endpoint=${DO_SPACES_ENDPOINT:-"https://$DO_SPACES_REGION.digitaloceanspaces.com"}
public=${FLOCKDECK_DL_URL:-https://dl.flockdeck.ai}
public=${public%/}

export AWS_ACCESS_KEY_ID="$DO_SPACES_KEY"
export AWS_SECRET_ACCESS_KEY="$DO_SPACES_SECRET"
# Spaces takes the region from the endpoint; this only has to be one the
# request can be signed for.
export AWS_DEFAULT_REGION="${AWS_DEFAULT_REGION:-us-east-1}"
# Since 2.23 the aws CLI adds checksums to every upload by default, which not
# every S3-compatible store accepts. Spaces and MinIO need none of them.
export AWS_REQUEST_CHECKSUM_CALCULATION=when_required
export AWS_RESPONSE_CHECKSUM_VALIDATION=when_required
export AWS_EC2_METADATA_DISABLED=true

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t publish-downloads)
trap 'rm -rf "$tmp"' EXIT
trap 'exit 1' INT TERM

# fetch copies one file of the bucket to $tmp, and fails if it is not there.
fetch() {
	aws s3 cp "s3://$bucket/$1" "$tmp/$2" --endpoint-url "$endpoint" --only-show-errors >/dev/null 2>&1
}

# A version's files are cached for a year and never change, so one already
# published with other files is refused: replacing them would leave the CDN's
# edges serving copies that disagree with the bucket for as long, and nothing
# purges them. Built again from the same tag the archives are the same, and
# uploading them again changes nothing. The manifest is kept as it was first
# published, though. Signed again it carries another date, and an edge
# holding the first manifest.json beside the second's signature would fail it.
keep_manifest=
if fetch "$version/checksums.txt" published.txt; then
	cmp -s "$tmp/published.txt" "$dist/checksums.txt" ||
		die "$version is already published with other files. Its files are cached for a year and are never purged, so replacing them would leave copies that disagree; tag a new version"
	say "$version is already published with these same files"
	if fetch "$version/manifest.json" manifest.json && fetch "$version/manifest.json.sig" manifest.json.sig; then
		keep_manifest=1
	fi
fi

content_type() {
	case "$1" in
		*.zip) echo application/zip ;;
		*.tar.gz) echo application/gzip ;;
		*.json) echo application/json ;;
		*) echo 'text/plain; charset=utf-8' ;;
	esac
}

# put uploads a file readable by anyone -- the bucket itself is private and
# cannot be listed, so only what is put here is public -- with how long it may
# be cached: Cache-Control for browsers and the CDN, and the max-age pairing
# DigitalOcean's CDN documents for its edge servers.
put() { # file key max-age cache-control
	say "uploading $2"
	aws s3 cp "$1" "s3://$bucket/$2" --endpoint-url "$endpoint" --only-show-errors \
		--acl public-read --content-type "$(content_type "$2")" \
		--cache-control "$4" --metadata "max-age=$3"
}
forever() { put "$1" "$2" 31536000 'public, max-age=31536000, immutable'; }
briefly() { put "$1" "$2" 60 'public, max-age=60'; }

# 1. The version's own files.
for f in "$@"; do
	forever "$f" "$version/${f##*/}"
done
forever "$dist/checksums.txt" "$version/checksums.txt"
forever "$dist/checksums.txt.sig" "$version/checksums.txt.sig"
if [ -n "$keep_manifest" ]; then
	say "keeping $version/manifest.json and its signature as they were first published"
else
	forever "$dist/manifest.json.sig" "$version/manifest.json.sig"
	forever "$dist/manifest.json" "$version/manifest.json"
fi

# 2. Read one back through the public address, as a person would, before
# anything points at it. A CDN can take a moment to reach a new file.
tries=0
until curl -fsS --connect-timeout 10 --max-time 60 -o "$tmp/served.txt" "$public/$version/checksums.txt" 2>/dev/null &&
	cmp -s "$tmp/served.txt" "$dist/checksums.txt"; do
	tries=$((tries + 1))
	[ "$tries" -lt 6 ] ||
		die "$public/$version/checksums.txt does not serve what was uploaded, so nothing has been moved to $version; check the CDN and the files' public-read ACL, then run this again"
	sleep "${FLOCKDECK_CHECK_WAIT:-5}"
done
say "$public/$version/checksums.txt serves what was uploaded"
# Nobody may list the bucket: the files are public, the index of them is not.
if curl -fsS --connect-timeout 10 --max-time 30 -o /dev/null "$public/" 2>/dev/null; then
	die "$public/ lists the bucket to anyone; make the bucket private (its files stay public-read), then run this again"
fi

if [ -n "$pre" ]; then
	say "$version is a pre-release: it is at $public/$version/, and latest.json and /latest/ are left as they are"
	exit 0
fi

# 3. The site's download buttons, under names without the version.
for f in "$@"; do
	name=${f##*/}
	briefly "$f" "latest/flockdeck_${name#flockdeck_"$version"_}"
done

# 4. The switch, last of all.
briefly "$dist/latest.json" latest.json
say "$version is the latest at $public/latest.json"
