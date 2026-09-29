#!/bin/sh
# Purge released versions from dl.flockdeck.ai: the bucket, and the caches in
# front of it, so that what was there cannot be downloaded from anywhere
# DigitalOcean or Cloudflare keeps a copy.
#
#   scripts/purge-downloads.sh [options] <version>...
#
#   --live                  delete and purge; without it nothing is changed
#   --confirm TEXT          a live run must say exactly "purge N versions",
#                           N being how many different versions it names
#   --max N                 how many versions one run may name; 100
#   --no-cloudflare         leave out the Cloudflare zone purge (see below)
#   --no-cdn-purge          leave out every CDN purge; what was cached then
#                           stays downloadable for up to a year
#   --json FILE             write the machine-readable summary to FILE too
#   --markdown FILE         write the report as Markdown to FILE
#   --check-input           only check the versions and --confirm, and say
#                           how many there are; needs no settings at all
#
# Versions may be given as separate arguments or several to an argument,
# separated by spaces or newlines. It needs only the aws CLI and curl, and any
# S3-compatible store will do, so it runs by hand as well as in the
# purge-downloads workflow.
#
# A DRY RUN IS THE DEFAULT. It reads the bucket, refuses what it would refuse
# for real, and reports every file it would delete and every address it would
# purge, and changes nothing.
#
# What is under a version is uploaded once, by publish-downloads.sh, and
# served "public, max-age=31536000, immutable": the CDN's edges keep it for a
# year. So deleting it from the bucket is not enough. A live run, for each
# version:
#
#   1. lists every version of every file under <version>/, and every delete
#      marker, since the bucket is versioned and a plain delete only hides a
#      file behind a marker and keeps its bytes;
#   2. deletes each of them by its version id, and lists again: anything
#      still there fails the run;
#
# and then, for every file it deleted:
#
#   3. purges DigitalOcean's CDN, by the file's path, and by <version>/* as
#      well, which also catches a copy cached under another query string;
#   4. purges the Cloudflare zone, by the file's exact public URL, 30 to a
#      request; and
#   5. asks for each exact public URL, as anyone would, and fails the run if
#      any is still served. The bucket is private, so a file that is gone is
#      answered 403 (or 404), never 200.
#
# A version with nothing left in the bucket is reported and not an error, so a
# run can be repeated. It is still purged, by prefix (<version>/* at
# DigitalOcean, <host>/<version>/ at Cloudflare), and its checksums.txt and
# manifest.json, which every release has, are checked gone: a run that deleted
# a version but then failed to purge it leaves nothing in the bucket to say
# which files the caches hold.
#
# Refused before anything is deleted, each saying why:
#
#   - anything that is not a version, such as v1.4.0 or v1.4.0-rc.1;
#   - the version latest.json names, and any version releases.json or
#     recalled.json names, when they exist; and a bucket without a readable
#     latest.json, since then the latest is not known;
#   - no versions at all, or more than --max; the same version twice is one;
#   - a file under a version whose name could not be purged or checked
#     exactly, which is anything but letters, digits and ._~+-/
#
# A deletion that fails stops the run from deleting any more versions, but
# what it did delete is still purged and checked, and the run fails. A purge
# that fails fails the run too: nothing reports success until every address
# has been seen gone.
#
# No secret is printed, and none is put on a command line where a process
# listing could show it: the API tokens reach curl through a header file only
# this run can read, and the Spaces key reaches the aws CLI through its
# environment, as in publish-downloads.sh.
#
# Cloudflare: dl.flockdeck.ai answers "Server: cloudflare" because
# DigitalOcean's CDN is served by Cloudflare, and step 3 purges that. Step 4
# is for a Cloudflare zone of flockdeck.ai's own in front of it. Where there
# is none, --no-cloudflare leaves it out, and step 5 still checks what anyone
# is served.
#
# Read from the environment:
#
#   DO_SPACES_KEY, DO_SPACES_SECRET  a key that can list, read and delete
#                                    the bucket
#   DO_SPACES_BUCKET                 the bucket
#   DO_SPACES_REGION                 its region, such as lon1
#   DO_SPACES_ENDPOINT               the S3 endpoint; by default
#                                    https://$DO_SPACES_REGION.digitaloceanspaces.com
#   DO_API_TOKEN                     a DigitalOcean API token with cdn:delete
#   DO_CDN_ENDPOINT_ID               the CDN endpoint in front of the bucket
#   CLOUDFLARE_API_TOKEN             a Cloudflare token with Cache Purge on
#   CLOUDFLARE_ZONE_ID               the zone dl.flockdeck.ai is in
#   FLOCKDECK_DL_URL                 where the bucket is served to everyone;
#                                    https://dl.flockdeck.ai by default
#
# and, for trying it against stand-ins, never set in the workflow:
#
#   FLOCKDECK_DO_API_URL             https://api.digitalocean.com
#   FLOCKDECK_CLOUDFLARE_API_URL     https://api.cloudflare.com/client/v4
#   FLOCKDECK_DO_PURGE_WAIT          seconds between DigitalOcean purges, which
#                                    it allows 50 files every 20 seconds; 20
#   FLOCKDECK_CHECK_WAIT             seconds between checks of the public
#                                    address; 10
#   FLOCKDECK_CHECK_TRIES            how many times each is checked; 12

set -eu

say() { printf 'purge-downloads: %s\n' "$*"; }
warn() { printf 'purge-downloads: WARNING: %s\n' "$*" >&2; }
die() { printf 'purge-downloads: %s\n' "$*" >&2; exit 1; }

usage="usage: $0 [--live --confirm 'purge N versions'] [--max N] [--no-cloudflare] [--no-cdn-purge] [--json FILE] [--markdown FILE] [--check-input] <version>..."

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t purge-downloads)
trap 'rm -rf "$tmp"' EXIT
trap 'exit 1' INT TERM
umask 077

live='' confirm='' confirmed='' max=100 no_cf='' no_cdn='' json_out='' md_out='' check_only=''
: >"$tmp/words"
while [ $# -gt 0 ]; do
	case $1 in
		--live) live=1 ;;
		--confirm | --max | --json | --markdown)
			[ $# -ge 2 ] || die "$1 needs a value; $usage"
			case $1 in
				--confirm) confirm=$2 confirmed=1 ;;
				--max)
					case $2 in '' | *[!0-9]*) die "--max takes a number, not $2" ;; esac
					max=$2 ;;
				--json) json_out=$2 ;;
				--markdown) md_out=$2 ;;
			esac
			shift ;;
		--no-cloudflare) no_cf=1 ;;
		--no-cdn-purge) no_cdn=1 ;;
		--check-input) check_only=1 ;;
		--) shift; break ;;
		-*) die "unknown option $1; $usage" ;;
		*) printf '%s\n' "$1" >>"$tmp/words" ;;
	esac
	shift
done
for arg in "$@"; do printf '%s\n' "$arg" >>"$tmp/words"; done

# One version to a line, in the order given, each once.
tr -s '[:space:]' '\n' <"$tmp/words" | awk 'NF && !seen[$0]++' >"$tmp/versions"
bad=$(grep -Evx 'v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?' "$tmp/versions" | tr '\n' ' ' || true)
[ -z "$bad" ] || die "not a version, so nothing has been purged: $bad(a version is v1.4.0, or v1.4.0-rc.1)"
count=$(wc -l <"$tmp/versions" | tr -d ' ')
[ "$count" -gt 0 ] || die "no versions named; $usage"
[ "$count" -le "$max" ] ||
	die "$count versions is more than one run may purge ($max); purge them in smaller runs, or say --max $count"
if [ -n "$confirmed" ] && [ "$confirm" != "purge $count versions" ]; then
	die "--confirm says \"$confirm\", but this names $count versions: it must say exactly \"purge $count versions\""
fi
if [ -n "$live" ] && [ -z "$confirmed" ]; then
	die "a live run must be confirmed: --confirm \"purge $count versions\""
fi
if [ -n "$check_only" ]; then
	say "$count versions: $(tr '\n' ' ' <"$tmp/versions")"
	exit 0
fi

# Everything missing is named at once, so one run says all there is to set.
# A dry run needs only the bucket, and names what a live run would miss.
need() {
	for name in "$@"; do
		[ -n "$(printenv "$name" || true)" ] || missing="$missing $name"
	done
}
missing=
need DO_SPACES_KEY DO_SPACES_SECRET DO_SPACES_BUCKET DO_SPACES_REGION
bucket_missing=$missing
[ -n "$no_cdn" ] || need DO_API_TOKEN DO_CDN_ENDPOINT_ID
[ -n "$no_cdn" ] || [ -n "$no_cf" ] || need CLOUDFLARE_API_TOKEN CLOUDFLARE_ZONE_ID
if [ -n "$live" ]; then
	[ -z "$missing" ] || die "not set:$missing"
else
	[ -z "$bucket_missing" ] || die "not set:$bucket_missing"
	[ -z "$missing" ] || warn "a live run would also need:$missing"
fi
for tool in aws curl; do
	command -v "$tool" >/dev/null 2>&1 || die "purging needs $tool, which is not installed"
done

bucket=$DO_SPACES_BUCKET
endpoint=${DO_SPACES_ENDPOINT:-"https://$DO_SPACES_REGION.digitaloceanspaces.com"}
public=${FLOCKDECK_DL_URL:-https://dl.flockdeck.ai}
public=${public%/}
host=${public#*://}
host=${host%%/*}
do_api=${FLOCKDECK_DO_API_URL:-https://api.digitalocean.com}
cf_api=${FLOCKDECK_CLOUDFLARE_API_URL:-https://api.cloudflare.com/client/v4}
do_wait=${FLOCKDECK_DO_PURGE_WAIT:-20}
check_wait=${FLOCKDECK_CHECK_WAIT:-10}
check_tries=${FLOCKDECK_CHECK_TRIES:-12}

export AWS_ACCESS_KEY_ID="$DO_SPACES_KEY"
export AWS_SECRET_ACCESS_KEY="$DO_SPACES_SECRET"
# As in publish-downloads.sh: Spaces takes the region from the endpoint, and
# not every S3-compatible store takes the checksums the aws CLI adds.
export AWS_DEFAULT_REGION="${AWS_DEFAULT_REGION:-us-east-1}"
export AWS_REQUEST_CHECKSUM_CALCULATION=when_required
export AWS_RESPONSE_CHECKSUM_VALIDATION=when_required
export AWS_EC2_METADATA_DISABLED=true
export AWS_PAGER=

# s3api runs one aws s3api call against the bucket, with its error kept in
# $tmp/aws.err.
s3api() {
	aws s3api "$@" --bucket "$bucket" --endpoint-url "$endpoint" 2>"$tmp/aws.err"
}
aws_error() { tr '\n' ' ' <"$tmp/aws.err"; }

# fetch copies one file of the bucket to $tmp, and fails if it is not there.
# Any other failure stops the run, as in publish-downloads.sh: a key that
# cannot read, taken for a missing latest.json or releases.json, would let
# through a version they name.
fetch() {
	if aws s3 cp "s3://$bucket/$1" "$tmp/$2" --endpoint-url "$endpoint" --only-show-errors >/dev/null 2>"$tmp/fetch.err"; then
		return 0
	fi
	if grep -Eq '\(404\)|NoSuchKey|does not exist|Not Found' "$tmp/fetch.err"; then
		return 1
	fi
	die "could not read $1 from the bucket, so what it protects is not known, and nothing has been purged: $(tr '\n' ' ' <"$tmp/fetch.err")"
}

# The versions that must stay, one to a line with why.
version_strings() { grep -Eo '"v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?"' "$1" | tr -d '"' || true; }
: >"$tmp/protected"
fetch latest.json latest.json ||
	die "the bucket has no latest.json, so which version is the latest is not known, and nothing has been purged"
latest=$(sed -n 's/.*"version" *: *"\([^"]*\)".*/\1/p' "$tmp/latest.json" | head -n 1)
printf '%s\n' "$latest" | grep -Eqx 'v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?' ||
	die "latest.json names no version, so which version is the latest is not known, and nothing has been purged"
printf '%s\tit is the latest release, named by latest.json\n' "$latest" >>"$tmp/protected"
for index in releases.json recalled.json; do
	if fetch "$index" "$index"; then
		version_strings "$tmp/$index" | while read -r v; do
			printf '%s\t%s names it\n' "$v" "$index"
		done >>"$tmp/protected"
	fi
done
refused=$(awk -F'\t' 'NR == FNR { why[$1] = why[$1] ? why[$1] "; " $2 : $2; next }
	$0 in why { printf "%s (%s)  ", $0, why[$0] }' "$tmp/protected" "$tmp/versions")
[ -z "$refused" ] || die "refusing, and nothing has been purged: $refused"

# list writes every version of every file under a release, and every delete
# marker, to $tmp/<name>, one "key<TAB>version id" to a line.
list() { # version name
	: >"$tmp/$2"
	for kind in Versions DeleteMarkers; do
		s3api list-object-versions --prefix "$1/" --output text --query "${kind}[].[Key,VersionId]" >"$tmp/listed" ||
			die "could not list $1/ in the bucket, so nothing more has been purged: $(aws_error)"
		# A line that is not a key and a version id, a key with a tab in it
		# say, is kept, as something that cannot be purged exactly: dropped,
		# it would neither be deleted nor be seen left behind.
		awk -F'\t' -v kind="$kind" '$0 == "None" || $0 == "" { next }
			NF == 2 { print $1 "\t" $2 "\t" kind; next }
			{ print "unreadable:" $0 "\t?\t" kind }' "$tmp/listed" >>"$tmp/$2"
	done
}

# The plan: what is under each version now.
mkdir "$tmp/v"
: >"$tmp/plan"
n=0
while read -r v; do
	n=$((n + 1))
	list "$v" "v/$n"
	unsafe=$(awk -F'\t' -v p="$v/" 'index($1, p) != 1 || $1 !~ /^[A-Za-z0-9._~+\/-]+$/ || $2 !~ /^[A-Za-z0-9._+\/=-]+$/ { print $1 }' "$tmp/v/$n" | head -n 3 | tr '\n' ' ')
	[ -z "$unsafe" ] ||
		die "$v holds a file whose name or version id could not be purged or checked exactly, so nothing has been purged: $unsafe"
	cut -f1 "$tmp/v/$n" | sort -u >"$tmp/v/$n.keys"
	keys=$(wc -l <"$tmp/v/$n.keys" | tr -d ' ')
	objects=$(awk -F'\t' '$3 == "Versions"' "$tmp/v/$n" | wc -l | tr -d ' ')
	markers=$(awk -F'\t' '$3 == "DeleteMarkers"' "$tmp/v/$n" | wc -l | tr -d ' ')
	printf '%s\t%s\t%s\t%s\t%s\n' "$n" "$v" "$keys" "$objects" "$markers" >>"$tmp/plan"
done <"$tmp/versions"

# status is what happened to each version, one "n<TAB>status" to a line.
: >"$tmp/status"
status_of() { awk -F'\t' -v n="$1" '$1 == n { s = $2 } END { print s }' "$tmp/status"; }

# report prints the per-version report, and writes the summaries.
report() { # outcome
	say "report ($1):"
	while IFS="$(printf '\t')" read -r n v keys objects markers; do
		st=$(status_of "$n")
		say "  $v: $st -- $keys files, $objects file versions, $markers delete markers"
		sed 's/^/purge-downloads:      /' "$tmp/v/$n.keys"
	done <"$tmp/plan"
	summary_json "$1" >"$tmp/summary.json"
	say "summary $(cat "$tmp/summary.json")"
	[ -z "$json_out" ] || cp "$tmp/summary.json" "$json_out"
	[ -z "$md_out" ] || summary_markdown "$1" >"$md_out"
}

json_list() { awk 'BEGIN { printf "[" } { printf "%s\"%s\"", (NR > 1 ? "," : ""), $0 } END { printf "]" }' "$1"; }
summary_json() {
	printf '{"mode":"%s","outcome":"%s","bucket":"%s","public":"%s","versions":[' \
		"$([ -n "$live" ] && echo live || echo dry-run)" "$1" "$bucket" "$public"
	first=1
	while IFS="$(printf '\t')" read -r n v keys objects markers; do
		[ -n "$first" ] || printf ','
		first=
		printf '{"version":"%s","status":"%s","files":%s,"file_versions":%s,"delete_markers":%s,"keys":%s}' \
			"$v" "$(status_of "$n")" "$keys" "$objects" "$markers" "$(json_list "$tmp/v/$n.keys")"
	done <"$tmp/plan"
	printf '],"cdn_purge":"%s","still_served":%s,"unconfirmed":%s}\n' "$cdn_state" \
		"$(json_list "$tmp/served")" "$(json_list "$tmp/unconfirmed")"
}
summary_markdown() {
	if [ -n "$live" ]; then echo "## Purge of $public: $1"; else echo "## Dry run of a purge of $public: $1"; fi
	echo
	[ -n "$live" ] || echo "Nothing was deleted or purged. Every file below is what a live run would delete."
	echo
	echo "| version | status | files | file versions | delete markers |"
	echo "| --- | --- | ---: | ---: | ---: |"
	while IFS="$(printf '\t')" read -r n v keys objects markers; do
		echo "| $v | $(status_of "$n") | $keys | $objects | $markers |"
	done <"$tmp/plan"
	echo
	echo "CDN purge: $cdn_state"
	if [ -s "$tmp/served" ] || [ -s "$tmp/unconfirmed" ]; then
		echo
		echo "Still served, or not confirmed gone:"
		echo
		sed 's/^/- /' "$tmp/served" "$tmp/unconfirmed"
	fi
	echo
	while IFS="$(printf '\t')" read -r n v keys objects markers; do
		[ "$keys" -gt 0 ] || continue
		echo "<details><summary>$v: $keys files</summary>"
		echo
		sed 's/^/- /' "$tmp/v/$n.keys"
		echo
		echo "</details>"
	done <"$tmp/plan"
}

: >"$tmp/served"
: >"$tmp/unconfirmed"
cdn_state="not attempted"
total=$(awk -F'\t' '{ s += $3 } END { print s + 0 }' "$tmp/plan")

if [ -z "$live" ]; then
	while IFS="$(printf '\t')" read -r n v keys objects markers; do
		if [ "$keys" -gt 0 ]; then
			printf '%s\twould delete\n' "$n" >>"$tmp/status"
		else
			printf '%s\tnothing in the bucket, would purge by prefix\n' "$n" >>"$tmp/status"
		fi
	done <"$tmp/plan"
	cdn_state="would purge $total files by address, and each version by prefix"
	[ -z "$no_cf" ] || cdn_state="$cdn_state, at DigitalOcean only (--no-cloudflare)"
	[ -z "$no_cdn" ] || cdn_state="would be left out (--no-cdn-purge)"
	report "dry run"
	say "dry run: nothing was deleted or purged; to purge these, run again with --live --confirm \"purge $count versions\""
	exit 0
fi

if [ -n "$no_cdn" ]; then
	warn "--no-cdn-purge: the CDN caches will NOT be purged. Everything under these versions was cached for a year, and stays downloadable from DigitalOcean's and Cloudflare's edges for up to a year after it is deleted here."
fi
if [ -n "$no_cf" ] && [ -z "$no_cdn" ]; then
	warn "--no-cloudflare: no Cloudflare zone is purged; DigitalOcean's CDN is, and every address is still checked gone"
fi

# 1 and 2. Delete, version by version. Deleting stops at the first version
# that fails, and what was deleted before it is still purged below.
failed=
: >"$tmp/paths"   # every file deleted, as a path in the bucket
: >"$tmp/absent"  # every version that was already gone
delete_batch() { # file of "key<TAB>version id" lines
	awk -F'\t' 'BEGIN { printf "{\"Objects\":[" }
		{ printf "%s{\"Key\":\"%s\",\"VersionId\":\"%s\"}", (NR > 1 ? "," : ""), $1, $2 }
		END { printf "],\"Quiet\":true}" }' "$1" >"$tmp/delete.json"
	if s3api delete-objects --delete "$(cat "$tmp/delete.json")" --output text --query 'Errors[].[Key,VersionId,Code]' >"$tmp/deleted"; then
		grep -v '^None$' "$tmp/deleted" | sed 's/^/purge-downloads:   could not delete /' >&2 || true
		return 0
	fi
	# Some S3-compatible stores refuse the checksum a newer aws CLI puts on a
	# multi-object delete. Deleting one at a time needs none; the list after
	# says whether it worked either way.
	say "  a batch delete was refused ($(aws_error)); deleting one at a time"
	while IFS="$(printf '\t')" read -r key id; do
		s3api delete-object --key "$key" --version-id "$id" >/dev/null ||
			printf 'purge-downloads:   could not delete %s %s: %s\n' "$key" "$id" "$(aws_error)" >&2
	done <"$1"
}
while IFS="$(printf '\t')" read -r n v keys objects markers; do
	if [ -n "$failed" ]; then
		printf '%s\tnot attempted, since an earlier version failed\n' "$n" >>"$tmp/status"
		continue
	fi
	if [ "$keys" -eq 0 ]; then
		say "$v: nothing in the bucket; purging it by prefix and checking it is gone"
		printf '%s\n' "$v" >>"$tmp/absent"
		printf '%s\tnothing in the bucket, purged by prefix\n' "$n" >>"$tmp/status"
		continue
	fi
	say "$v: deleting $objects file versions and $markers delete markers of $keys files"
	cut -f1,2 "$tmp/v/$n" | split -l 250 - "$tmp/batch.$n."
	for b in "$tmp/batch.$n."*; do
		delete_batch "$b"
	done
	cat "$tmp/v/$n.keys" >>"$tmp/paths"
	list "$v" "left.$n"
	if [ -s "$tmp/left.$n" ]; then
		say "$v: still in the bucket after deleting:"
		sed 's/^/purge-downloads:   /' "$tmp/left.$n"
		printf '%s\tFAILED: %s file versions or delete markers are still in the bucket\n' "$n" "$(wc -l <"$tmp/left.$n" | tr -d ' ')" >>"$tmp/status"
		failed=1
		continue
	fi
	say "$v: nothing is left in the bucket"
	printf '%s\tdeleted\n' "$n" >>"$tmp/status"
done <"$tmp/plan"

# What the caches are purged of, and what is checked gone: every file
# deleted, and for a version already gone, every release's two fixed files.
cp "$tmp/paths" "$tmp/check"
while read -r v; do
	printf '%s/checksums.txt\n%s/manifest.json\n' "$v" "$v" >>"$tmp/check"
done <"$tmp/absent"
awk -F'\t' '{ print $2 }' "$tmp/plan" >"$tmp/attempted"
[ -z "$failed" ] || awk -F'\t' 'NR == FNR { if ($2 ~ /^not attempted/) skip[$1] = 1; next } !($1 in skip) { print $2 }' "$tmp/status" "$tmp/plan" >"$tmp/attempted"

# api sends one request to a purge API, with the token read by curl from a
# header file, and retries while it is told to slow down. It leaves the
# answer in $tmp/answer and its status in $code.
api() { # header-file method url body-file
	tries=0
	while :; do
		code=$(curl -sS --connect-timeout 10 --max-time 60 -o "$tmp/answer" -w '%{http_code}' \
			-X "$2" -H @"$1" -H 'Content-Type: application/json' --data-binary @"$4" "$3" 2>"$tmp/curl.err") || code=000
		[ "$code" = 429 ] || return 0
		tries=$((tries + 1))
		[ "$tries" -lt 6 ] || return 0
		say "  told to slow down; waiting"
		sleep "$(( do_wait > 0 ? do_wait : 1 ))"
	done
}
answer() { printf '%s %s' "$(head -c 500 "$tmp/answer" 2>/dev/null | tr '\n' ' ')" "$(tr '\n' ' ' <"$tmp/curl.err")"; }
body() { # key file-of-strings
	awk -v k="$1" 'BEGIN { printf "{\"%s\":[", k } { printf "%s\"%s\"", (NR > 1 ? "," : ""), $0 } END { printf "]}" }' "$2"
}

purge_failed=
if [ -n "$no_cdn" ]; then
	cdn_state="left out (--no-cdn-purge): cached copies stay downloadable for up to a year"
else
	cdn_state="purged"
	# 3. DigitalOcean's CDN, by path, 50 to a request and one request every
	# 20 seconds, which is as many files as it allows purged in that time.
	printf 'Authorization: Bearer %s\n' "$DO_API_TOKEN" >"$tmp/do.header"
	{
		cat "$tmp/paths"
		sed 's|$|/*|' "$tmp/attempted"
	} >"$tmp/do.files"
	split -l 50 "$tmp/do.files" "$tmp/do.batch."
	sent=0
	for b in "$tmp/do.batch."*; do
		[ -s "$b" ] || continue
		[ "$sent" -eq 0 ] || sleep "$do_wait"
		body files "$b" >"$tmp/do.json"
		api "$tmp/do.header" DELETE "$do_api/v2/cdn/endpoints/$DO_CDN_ENDPOINT_ID/cache" "$tmp/do.json"
		sent=$((sent + 1))
		case $code in
			2??) say "DigitalOcean CDN: purged $(wc -l <"$b" | tr -d ' ') paths" ;;
			*)
				printf 'purge-downloads: DigitalOcean CDN purge FAILED (HTTP %s): %s\n' "$code" "$(answer)" >&2
				purge_failed=1 ;;
		esac
	done
	rm -f "$tmp/do.header"

	# 4. The Cloudflare zone, by exact URL, 30 to a request; a version
	# already gone by prefix, as the zone's purge takes it: host and path.
	if [ -z "$no_cf" ]; then
		printf 'Authorization: Bearer %s\n' "$CLOUDFLARE_API_TOKEN" >"$tmp/cf.header"
		sed "s|^|$public/|" "$tmp/paths" >"$tmp/cf.files"
		sed "s|^|$host/|; s|$|/|" "$tmp/absent" >"$tmp/cf.prefixes"
		split -l 30 "$tmp/cf.files" "$tmp/cf.batch.files."
		split -l 30 "$tmp/cf.prefixes" "$tmp/cf.batch.prefixes."
		for b in "$tmp/cf.batch."*; do
			[ -s "$b" ] || continue
			case $b in *.prefixes.*) kind=prefixes ;; *) kind=files ;; esac
			body "$kind" "$b" >"$tmp/cf.json"
			api "$tmp/cf.header" POST "$cf_api/zones/$CLOUDFLARE_ZONE_ID/purge_cache" "$tmp/cf.json"
			if [ "$code" = 200 ] && grep -Eq '"success"[[:space:]]*:[[:space:]]*true' "$tmp/answer"; then
				say "Cloudflare: purged $(wc -l <"$b" | tr -d ' ') $kind"
			else
				printf 'purge-downloads: Cloudflare purge FAILED (HTTP %s): %s\n' "$code" "$(answer)" >&2
				purge_failed=1
			fi
		done
		rm -f "$tmp/cf.header"
	else
		cdn_state="purged at DigitalOcean; no Cloudflare zone (--no-cloudflare)"
	fi
	[ -z "$purge_failed" ] || cdn_state="FAILED: a purge request was refused"
fi

# 5. Every address, asked for as anyone would. A purge takes a moment to
# reach every edge, so each is asked again until it is gone or the tries run
# out. Only 403 or 404 is gone; anything else, a timeout or a 5xx included,
# is not known to be.
if [ -n "$no_cdn" ]; then
	warn "the public addresses are not checked, since nothing was purged from the CDN"
else
	sed "s|^|$public/|" "$tmp/check" >"$tmp/pending"
	round=0
	while [ -s "$tmp/pending" ]; do
		round=$((round + 1))
		: >"$tmp/served"
		: >"$tmp/unconfirmed"
		while read -r url; do
			got=$(curl -s -o /dev/null -w '%{http_code}' -r 0-0 --connect-timeout 10 --max-time 30 "$url" 2>/dev/null) || got=000
			case $got in
				403 | 404) ;;
				2??) printf '%s (HTTP %s)\n' "$url" "$got" >>"$tmp/served" ;;
				*) printf '%s (HTTP %s)\n' "$url" "$got" >>"$tmp/unconfirmed" ;;
			esac
		done <"$tmp/pending"
		sed 's/ (HTTP [0-9]*)$//' "$tmp/served" "$tmp/unconfirmed" >"$tmp/pending"
		[ -s "$tmp/pending" ] && [ "$round" -lt "$check_tries" ] || break
		say "$(wc -l <"$tmp/pending" | tr -d ' ') addresses not gone yet; asking again"
		sleep "$check_wait"
	done
	gone=$(($(wc -l <"$tmp/check") - $(wc -l <"$tmp/pending")))
	say "$gone of $(wc -l <"$tmp/check" | tr -d ' ') addresses answer 403 or 404 at $public"
fi

if [ -n "$failed" ] || [ -n "$purge_failed" ] || [ -s "$tmp/served" ] || [ -s "$tmp/unconfirmed" ]; then
	report FAILED
	[ ! -s "$tmp/served" ] || {
		printf 'purge-downloads: STILL SERVED at %s:\n' "$public" >&2
		sed 's/^/purge-downloads:   /' "$tmp/served" >&2
	}
	[ ! -s "$tmp/unconfirmed" ] || {
		printf 'purge-downloads: not confirmed gone:\n' >&2
		sed 's/^/purge-downloads:   /' "$tmp/unconfirmed" >&2
	}
	die "FAILED: the purge is not complete; see above. Running it again with the same versions is safe."
fi
report "done"
if [ -n "$no_cdn" ]; then
	warn "deleted from the bucket, but the CDN was not purged: cached copies stay downloadable for up to a year"
else
	say "done: $count versions are gone from the bucket and the CDN, and every address checked answers 403 or 404"
fi
