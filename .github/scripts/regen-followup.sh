#!/bin/sh
# Regenerates one of the two sites that are built from this repository,
# flockdeck-site or flockdeck-docs, for a release, and if -- and only if --
# that changes anything, puts the change into a single pull request and
# gets it merged. See .github/RELEASE_AUTOMATION_SETUP.md.
#
# It is what release.yml's followups job runs, and what
# release-followups-dry-run.yml runs with MODE=dry-run, which pushes nothing
# and touches no other repository. It is kept here, a script, rather than
# written into a workflow, so that it can be run by hand and tried against
# fixtures (cmd/release/followup_test.go).
#
# Everything comes in through the environment, never through arguments and
# never spliced into a command by a workflow, so that nothing a tag or an API
# answer says can be read as shell:
#
#   TAG          the release, v1.2.3. Refused unless it is exactly that.
#   TARGET       site or docs.
#   TARGET_DIR   a checkout of the target repository's main branch.
#   TARGET_REPO  owner/name of that repository, for gh.
#   SRC_DIR      a checkout of this repository at TAG. Default: the current directory.
#   CHECKSUMS    site only: that release's checksums.txt, with checksums.txt.sig
#                beside it. sitegen checks the signature.
#   MODE         apply, or dry-run. Default: dry-run.
#   AUTO_MERGE   AUTO_MERGE_REGEN, the repository variable. Off when it says
#                false, 0, no or off; on when unset or anything else.
#   GH_TOKEN     an installation token for the target repository (apply only).
#   BOT_NAME, BOT_EMAIL  who the commit is by.
#
# Exit status: 0 when there was nothing to do, when the change was only
# reported (dry-run), and when the pull request was opened, and merged or left
# to merge; 1 when something could not be done, with the reason said. The
# release workflow does not depend on the answer.
set -eu

BRANCH_PREFIX=auto/regen-
BASE_BRANCH=${BASE_BRANCH:-main}
# The one check both sites' pull requests must pass. Named here for the
# repository that has no branch protection to name it (flockdeck-site, private,
# on a plan without it), where merging when it passes is this script's job.
REQUIRED_CHECK=${REQUIRED_CHECK:-Check the site}
WAIT_SECONDS=${WAIT_SECONDS:-1800}
POLL_SECONDS=${POLL_SECONDS:-30}

say() { printf '%s\n' "$*"; }
warn() { printf '::warning::%s\n' "$*"; }
fail() {
	printf '::error::%s\n' "$*" >&2
	exit 1
}

# output writes name=value for the workflow, when it is one running this.
output() {
	if [ -n "${GITHUB_OUTPUT:-}" ]; then
		printf '%s=%s\n' "$1" "$2" >> "$GITHUB_OUTPUT"
	fi
}

# summary adds a line to the run's summary page, when there is one.
summary() {
	if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
		printf '%s\n' "$*" >> "$GITHUB_STEP_SUMMARY"
	fi
}

# valid_tag: a release, not a candidate. Candidates (v1.2.3-rc.1) are never
# published as latest, so nothing follows them.
valid_tag() {
	# grep matches by line, so a value of two lines would pass if either were
	# a version; anything that is not made of a version's characters is
	# refused first.
	case $1 in *[!v0-9.]*) return 1 ;; esac
	printf '%s\n' "$1" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'
}

# auto_merge_on reads AUTO_MERGE_REGEN. Unset is on: the point of the switch is
# to be able to turn this off, not to have to turn it on.
auto_merge_on() {
	case $(printf '%s' "${AUTO_MERGE:-}" | tr '[:upper:]' '[:lower:]') in
		false | 0 | no | off) return 1 ;;
	esac
	return 0
}

# git_t runs git in the target checkout.
git_t() { git -C "$TARGET_DIR" "$@"; }

# git_net_t talks to the remote with the installation token, which git reads
# from the environment when it asks, so that it is on no command line and in
# no file.
# shellcheck disable=SC2016 # the helper's $GH_TOKEN is git's shell's to expand
git_net_t() {
	git_t -c credential.helper= \
		-c 'credential.helper=!f() { echo username=x-access-token; echo "password=$GH_TOKEN"; }; f' \
		"$@"
}

# generate writes the site into TARGET_DIR, as the release's own generator
# writes it. The generator is the tag's code, and is run without the
# installation token in its environment: only git and gh, below, have it.
generate() {
	case $TARGET in
		site)
			[ -f "${CHECKSUMS:-}" ] || fail "no checksums.txt at '${CHECKSUMS:-}'"
			[ -f "$CHECKSUMS.sig" ] || fail "no checksums.txt.sig beside $CHECKSUMS"
			(cd "$SRC_DIR" && env -u GH_TOKEN -u GITHUB_TOKEN go run ./cmd/sitegen -release "$TAG" -checksums "$CHECKSUMS" -out "$TARGET_DIR")
			;;
		docs)
			(cd "$SRC_DIR" && env -u GH_TOKEN -u GITHUB_TOKEN go run ./cmd/docgen -out "$TARGET_DIR")
			;;
	esac
}

# pr_body says what this is, and why it may merge itself.
pr_body() {
	src=$(git -C "$SRC_DIR" rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
	runline=""
	if [ -n "${GITHUB_RUN_ID:-}" ]; then
		runline="Run: ${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY:-}/actions/runs/$GITHUB_RUN_ID"
	fi
	case $TARGET in
		site) cmd="sitegen -release $TAG -checksums checksums.txt" ;;
		docs) cmd="docgen" ;;
	esac
	cat <<EOF
Regenerated for **$TAG**, from Flockdeck/flockdeck at \`$src\`, by \`go run ./cmd/$cmd\`.
$runline

Diff stat against \`$BASE_BRANCH\`:

\`\`\`
$STAT
\`\`\`

**Why this may merge itself.** Nothing here was written by hand. It is the
output of a generator whose source was already reviewed and released in
Flockdeck/flockdeck, and it is only opened when it differs from \`$BASE_BRANCH\`.
EOF
	if [ "$TARGET" = site ]; then
		cat <<'EOF'
sitegen also checks the release's checksums.txt against its signature, with the
release keys built into it, before pinning install.sh and install.ps1 to that
release, so a checksum file that was tampered with is refused rather than
written into a script that people pipe into a shell.
EOF
	fi
	cat <<EOF

It merges when \`$REQUIRED_CHECK\` passes. Merging to \`$BASE_BRANCH\` deploys
nothing by itself; the next patch tag does, and the repository's auto-tag
workflow pushes it once CI on the merge commit is green. To stop this
automation from merging, set the \`AUTO_MERGE_REGEN\` variable in
Flockdeck/flockdeck to \`false\`; the pull request is then opened and left.
EOF
}

# find_pr prints the number of the open pull request for the branch, or
# nothing.
find_pr() {
	gh pr list --repo "$TARGET_REPO" --head "$1" --base "$BASE_BRANCH" --state open \
		--json number --jq '.[0].number // empty'
}

# CHECKS_JQ adds a commit's check runs up to pass, pending or fail. A required
# check that has not shown up yet is pending, not a pass, and a failure is a
# failure while others are still running.
# shellcheck disable=SC2016 # jq's variables, not the shell's
CHECKS_JQ='
	.check_runs as $c
	| if any($c[]; .status == "completed" and (.conclusion | IN("success", "skipped", "neutral") | not)) then "fail"
	  elif any($c[]; .status != "completed") then "pending"
	  elif any($c[]; .name == $required and .conclusion == "success") then "pass"
	  else "pending" end'

checks_state() {
	gh api "repos/$TARGET_REPO/commits/$1/check-runs?per_page=100" | jq -r --arg required "$REQUIRED_CHECK" "$CHECKS_JQ"
}

# wait_and_merge is for a repository where --auto has nothing to wait on, or
# is not allowed: it waits for the checks itself, and merges only the commit
# it waited for, so a push in between is not merged unseen.
wait_and_merge() {
	pr=$1 sha=$2
	waited=0
	while :; do
		state=$(checks_state "$sha") || state=pending
		case $state in
			pass)
				gh pr merge "$pr" --repo "$TARGET_REPO" --squash --match-head-commit "$sha"
				say "merged #$pr"
				return 0
				;;
			fail) fail "checks failed on #$pr ($TARGET_REPO); it is left open" ;;
		esac
		if [ "$waited" -ge "$WAIT_SECONDS" ]; then
			fail "'$REQUIRED_CHECK' did not pass on #$pr ($TARGET_REPO) within ${WAIT_SECONDS}s; it is left open"
		fi
		sleep "$POLL_SECONDS"
		waited=$((waited + POLL_SECONDS))
	done
}

# merge_pr turns on auto-merge, which lets the required check decide, and
# falls back to waiting for the checks when GitHub will not have it: the
# repository has not allowed auto-merge, or the branch is unprotected and there
# is no required check for it to wait for (it would merge at once).
merge_pr() {
	pr=$1 sha=$2
	if ! auto_merge_on; then
		say "auto-merge is off (AUTO_MERGE_REGEN); #$pr is left open"
		summary "Auto-merge is off; $TARGET_REPO#$pr is left open."
		return 0
	fi
	if gh pr merge "$pr" --repo "$TARGET_REPO" --auto --squash --match-head-commit "$sha" 2> "${TMPDIR:-/tmp}/regen-merge.err"; then
		say "auto-merge is on for #$pr"
		return 0
	fi
	cat "${TMPDIR:-/tmp}/regen-merge.err" >&2
	warn "$TARGET_REPO cannot auto-merge #$pr, so it is waited for here instead"
	wait_and_merge "$pr" "$sha"
}

main() {
	: "${TAG:?TAG is required}" "${TARGET:?TARGET is required (site or docs)}"
	: "${TARGET_DIR:?TARGET_DIR is required}"
	MODE=${MODE:-dry-run}
	SRC_DIR=${SRC_DIR:-.}
	valid_tag "$TAG" || fail "'$TAG' is not a release version (v1.2.3); candidates are not followed up"
	case $TARGET in site | docs) ;; *) fail "TARGET must be site or docs" ;; esac
	case $MODE in apply | dry-run) ;; *) fail "MODE must be apply or dry-run" ;; esac
	[ "$MODE" = apply ] && { : "${TARGET_REPO:?TARGET_REPO is required}" "${GH_TOKEN:?GH_TOKEN is required to apply}"; }
	TARGET_DIR=$(cd "$TARGET_DIR" && pwd)
	SRC_DIR=$(cd "$SRC_DIR" && pwd)
	[ -z "${CHECKSUMS:-}" ] || CHECKSUMS=$(cd "$(dirname "$CHECKSUMS")" && pwd)/$(basename "$CHECKSUMS")

	# A checkout that is not clean would put whatever is in it into the pull
	# request.
	[ -z "$(git_t status --porcelain)" ] || fail "$TARGET_DIR is not clean before generating"

	generate

	# What differs is what git says differs once the files are added, not
	# what its stat cache says: a checkout with CRLF in it (a Windows one)
	# shows every generated file as modified though it is not, and one whose
	# files were only rewritten shows the same.
	git_t add -A
	if git_t diff --cached --quiet; then
		say "$TARGET is already what $TAG generates: nothing to do"
		summary "**$TARGET** at $TAG: no diff, nothing to do."
		output changed false
		# A pull request left open from an earlier run, for changes that
		# have since reached the branch some other way, has nothing in it.
		if [ "$MODE" = apply ]; then
			old=$(find_pr "$BRANCH_PREFIX$TAG" || true)
			if [ -n "$old" ]; then
				gh pr close "$old" --repo "$TARGET_REPO" \
					--comment "Regenerating for $TAG now changes nothing, so this has nothing left to merge."
			fi
		fi
		return 0
	fi

	STAT=$(git_t diff --cached --stat | sed 's/^ *//')
	say "$TARGET differs from what $TAG generates:"
	say "$STAT"
	output changed true
	if [ "$MODE" = dry-run ]; then
		summary "**$TARGET** at $TAG: DIFF, dry run, nothing pushed."
		summary '```'
		summary "$STAT"
		summary '```'
		git_t reset -q
		return 0
	fi

	branch=$BRANCH_PREFIX$TAG
	git_t config user.name "${BOT_NAME:-flockdeck-release-bot[bot]}"
	git_t config user.email "${BOT_EMAIL:-flockdeck-release-bot[bot]@users.noreply.github.com}"
	git_t checkout -q -B "$branch"
	git_t commit -q -m "Regenerate for $TAG"
	mine=$(git_t rev-parse HEAD)

	# The branch is force-pushed, but not when it already holds exactly this
	# content: a new commit has a new hash whatever it holds, and pushing it
	# would start the target's CI again for nothing.
	pushed=false
	theirs=""
	if git_net_t ls-remote --exit-code --heads origin "$branch" > /dev/null 2>&1; then
		git_net_t fetch -q origin "$branch"
		theirs=$(git_t rev-parse FETCH_HEAD)
	fi
	if [ -n "$theirs" ] && [ "$(git_t rev-parse "$theirs^{tree}")" = "$(git_t rev-parse "$mine^{tree}")" ]; then
		say "$branch already holds this change"
		sha=$theirs
	else
		git_net_t push --force origin "$branch"
		pushed=true
		sha=$mine
	fi

	pr=$(find_pr "$branch")
	title="Regenerate for $TAG"
	body=$(mktemp)
	pr_body > "$body"
	if [ -z "$pr" ]; then
		url=$(gh pr create --repo "$TARGET_REPO" --base "$BASE_BRANCH" --head "$branch" --title "$title" --body-file "$body")
		pr=${url##*/}
		say "opened $url"
	elif [ "$pushed" = true ]; then
		gh pr edit "$pr" --repo "$TARGET_REPO" --title "$title" --body-file "$body"
		say "updated #$pr"
	else
		say "#$pr is already open and up to date"
	fi
	rm -f "$body"
	summary "**$TARGET** at $TAG: $TARGET_REPO#$pr"
	output pr "$pr"

	merge_pr "$pr" "$sha"
}

# Sourced by the tests, which call the functions one at a time.
if [ -z "${REGEN_SOURCED:-}" ]; then
	main "$@"
fi
