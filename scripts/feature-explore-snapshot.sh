#!/usr/bin/env bash
#
# feature-explore-snapshot.sh — read-only evidence snapshot for feature discovery.
#
# Usage:
#   ./scripts/feature-explore-snapshot.sh [--since YYYY-MM-DD] [--no-github] [--repo <path>]
#
# Prints, as plain labelled sections, what the feature-explore skill
# (.agents/skills/feature-explore/SKILL.md) needs from git, worktrees and GitHub
# in one stable command. Discovery reports and archived plans are untracked
# and live in the planning checkout (the worktree on dev, ori-agent-dev), so
# both are read from there whichever checkout runs this. `--repo` names another
# checkout (default: this script's repository); `--since` overrides the window
# the previous report sets; `--no-github` skips every gh call.
#
# Sections, in order: the reports folder, the previous report (the newest
# dated before today) and any written today; every worktree with its role,
# last commit, lead over origin/dev, changed files, the commits it carries,
# open checklists with their next unticked item, other planning files and its
# branch's PR state; branches whose name carries an Issue number; archived
# plans; open and merged PRs to dev; open Issues; commits, most-changed paths
# and longest-untouched packages since the previous report; open Issue bodies,
# each cut to 800 characters (`gh issue view` has the rest); and a status
# block that is always last, so output without it was truncated.
#
# It writes nothing and changes nothing: no fetch, so origin/dev is as of the
# last fetch; no Issue, PR, branch or file writes.
#
# Exit codes: 0 complete; 1 GitHub could not answer (those sections say
# UNAVAILABLE and the run is incomplete); 2 wrong invocation.

set -euo pipefail
export LC_ALL=C

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(CDPATH= cd -- "$script_dir/.." && pwd -P)"
since=""
use_github=1
github_failed=0
today="$(date +%F)"

usage() {
	echo "usage: $0 [--since YYYY-MM-DD] [--no-github] [--repo <path>]" >&2
	exit 2
}

while (($# > 0)); do
	case "$1" in
	--since)
		[[ $# -ge 2 && "$2" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || usage
		since="$2"
		shift 2
		;;
	--no-github)
		use_github=0
		shift
		;;
	--repo)
		[[ $# -ge 2 && -d "$2" ]] || usage
		repo_root="$(CDPATH= cd -- "$2" && pwd -P)"
		shift 2
		;;
	*) usage ;;
	esac
done

cd "$repo_root"
if ! git rev-parse --show-toplevel >/dev/null 2>&1; then
	echo "not a git checkout: $repo_root" >&2
	exit 2
fi

section() {
	printf '\n== %s ==\n' "$1"
}

or_none() {
	if [[ -n "$1" ]]; then
		printf '%s\n' "$1"
	else
		echo "none"
	fi
}

base_ref="origin/dev"
base_note=""
if ! git rev-parse --verify --quiet "$base_ref" >/dev/null; then
	base_ref="HEAD"
	base_note=" (origin/dev not present; using HEAD)"
fi

section "snapshot"
echo "date: $today"
echo "repo: $repo_root"
echo "branch: $(git rev-parse --abbrev-ref HEAD)"
echo "head: $(git rev-parse --short HEAD)"
echo "base: $base_ref$base_note, as of the last fetch (this script never fetches)"

planning_root="$(git worktree list --porcelain |
	awk '/^worktree / { p = substr($0, 10) } /^branch refs\/heads\/dev$/ { print p; exit }')"
if [[ -z "$planning_root" || ! -d "$planning_root" ]]; then
	planning_root="$repo_root"
fi
reports_dir="$planning_root/docs/feature-discovery/reports"

section "reports"
echo "dir: $reports_dir (write this run's report here)"
shopt -s nullglob
reports=("$reports_dir"/[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]*.md)
shopt -u nullglob
# Byte order (LC_ALL=C) puts YYYY-MM-DD.md after YYYY-MM-DD-<scope>.md, so a
# day's full run is that day's last entry and wins as the previous report.
previous=""
for report in "${reports[@]}"; do
	report_date="$(basename "$report")"
	report_date="${report_date:0:10}"
	if [[ "$report_date" < "$today" ]]; then
		previous="$report"
	elif [[ "$report_date" == "$today" ]]; then
		echo "today: $report"
	fi
done
if [[ -n "$previous" ]]; then
	echo "previous: $previous"
	if [[ -z "$since" ]]; then
		since="$(basename "$previous")"
		since="${since:0:10}"
	fi
else
	echo "previous: none"
fi
if [[ -z "$since" ]]; then
	since="$(date -v-90d +%F 2>/dev/null || date -d '90 days ago' +%F)"
	echo "window: no previous report, defaulting to 90 days"
fi
echo "since: $since"

# Sets pr_result to "#N STATE" for the newest PR whose head is $1, "none", or
# "UNAVAILABLE". A global, not command substitution, so a failure can reach
# github_failed.
pr_result=""
pr_state() {
	local out
	if ((use_github == 0)); then
		pr_result="skipped"
		return 0
	fi
	if out="$(gh pr list --state all --head "$1" --limit 1 --json number,state \
		--jq '.[] | "#\(.number) \(.state)"' 2>/dev/null)"; then
		pr_result="${out:-none}"
	else
		github_failed=1
		pr_result="UNAVAILABLE"
	fi
}

section "worktrees"
wt_path=""
wt_branch=""
print_worktree() {
	[[ -n "$wt_path" ]] || return 0
	local role="feature" last ahead changes untracked modified plans plan ticked total next others
	case "$wt_branch" in
	dev) role="planning" ;;
	main) role="release" ;;
	"") role="detached" ;;
	esac
	if [[ ! -d "$wt_path" ]]; then
		echo "- $wt_path  branch=${wt_branch:-detached}  role=$role  MISSING (directory gone; housekeeping)"
		return 0
	fi
	last="$(git -C "$wt_path" log -1 --format=%ad --date=short 2>/dev/null || echo '?')"
	ahead="$(git -C "$wt_path" rev-list --count "$base_ref..HEAD" 2>/dev/null || echo '?')"
	changes="$(git -C "$wt_path" status --porcelain 2>/dev/null || true)"
	untracked="$(grep -c '^??' <<<"$changes" || true)"
	modified="$(grep -vc '^??' <<<"$changes" || true)"
	[[ -n "$changes" ]] || modified=0
	pr_result="n/a"
	if [[ "$role" == feature ]]; then
		pr_state "$wt_branch"
	fi
	echo "- $wt_path  branch=${wt_branch:-detached}  role=$role  last_commit=$last  ahead_of_base=$ahead  modified=${modified:-0}  untracked=${untracked:-0}  pr=$pr_result"
	if [[ "$role" == feature ]]; then
		git -C "$wt_path" log --format='    commit: %h %s' "$base_ref..HEAD" 2>/dev/null | awk 'NR <= 5' || true
	fi
	# The release checkout's tasks/ holds long-dead plans; skip it.
	[[ "$role" != release && -d "$wt_path/tasks" ]] || return 0
	plans="$(find "$wt_path/tasks" -maxdepth 1 -type f \( -name 'prd-*.md' -o -name 'tasks-*.md' \) ! -name '*(done*' 2>/dev/null | sort || true)"
	if [[ -n "$plans" ]]; then
		while IFS= read -r plan; do
			total="$(grep -c '^[[:space:]]*- \[[ xX]\]' "$plan" 2>/dev/null || true)"
			if ((${total:-0} == 0)); then
				echo "    tasks/$(basename "$plan")"
				continue
			fi
			ticked="$(grep -c '^[[:space:]]*- \[[xX]\]' "$plan" 2>/dev/null || true)"
			next="$(awk '/^[[:space:]]*- \[ \]/ { sub(/^[[:space:]]*- \[ \][[:space:]]*/, ""); print substr($0, 1, 120); exit }' "$plan")"
			echo "    tasks/$(basename "$plan")  ticked=${ticked:-0}/$total${next:+  next: $next}"
		done <<<"$plans"
	fi
	others="$(find "$wt_path/tasks" -maxdepth 1 -type f -name '*.md' ! -name 'prd-*' ! -name 'tasks-*' ! -name '*(done*' 2>/dev/null |
		awk -F/ '{ print $NF }' | sort | paste -sd, - | awk '{ gsub(/,/, ", "); print }' || true)"
	if [[ -n "$others" ]]; then
		echo "    also: $others"
	fi
}
while IFS= read -r line; do
	case "$line" in
	"worktree "*)
		print_worktree
		wt_path="${line#worktree }"
		wt_branch=""
		;;
	"branch refs/heads/"*)
		wt_branch="${line#branch refs/heads/}"
		;;
	esac
done < <(git worktree list --porcelain)
print_worktree

section "branches carrying an Issue number"
or_none "$(git branch -a --format='%(refname:short)' | grep -E '(^|/)[0-9]+-' | sort -u || true)"

# `wt done` files them under tasks/archive/; archives from before that folder
# existed sit in tasks/ itself under the same "(done #N)" label.
section "archived plans in the planning checkout's tasks/archive/"
or_none "$(find "$planning_root/tasks/archive" "$planning_root/tasks" -maxdepth 1 \( -name 'prd-*' -o -name 'tasks-*' \) -name '*(done*' 2>/dev/null | awk -F/ '{ print $NF }' | sort -u || true)"

gh_section() {
	local title="$1" out
	shift
	section "$title"
	if ((use_github == 0)); then
		echo "skipped (--no-github)"
		return 0
	fi
	if out="$(gh "$@" 2>&1)"; then
		or_none "$out"
	else
		github_failed=1
		echo "UNAVAILABLE: ${out:-gh failed}"
	fi
}

gh_section "open PRs to dev" pr list --state open --base dev --limit 100 \
	--json number,title,headRefName,url,updatedAt \
	--jq '.[] | "#\(.number)  \(.headRefName)  \(.title)  \(.url)  updated=\(.updatedAt[:10])"'

merged_filter='.[] | select(.mergedAt >= "'"$since"'") | "#\(.number)  \(.mergedAt[:10])  \(.title)  \(.headRefName)"'
gh_section "merged PRs to dev since $since" pr list --state merged --base dev --limit 100 \
	--json number,title,headRefName,mergedAt \
	--jq "$merged_filter"

gh_section "open Issues" issue list --state open --limit 1000 \
	--json number,title,labels,url,createdAt,updatedAt \
	--jq '.[] | "#\(.number)  [\([.labels[].name] | join(","))]  \(.title)  \(.url)  created=\(.createdAt[:10]) updated=\(.updatedAt[:10])"'

section "commits on $base_ref since $since"
or_none "$(git log "$base_ref" --since="$since" --date=short --format='%h %ad %s' | awk 'NR <= 200')"

section "most-changed paths since $since"
or_none "$(git log "$base_ref" --since="$since" --name-only --format= |
	awk 'NF { if (index($0, "/")) { sub(/\/[^\/]*$/, ""); print } else { print "(root)" } }' |
	sort | uniq -c | sort -rn | awk 'NR <= 25')"

section "longest-untouched internal packages"
untouched=""
for d in internal/*/; do
	d="${d%/}"
	last="$(git log -1 --format=%ad --date=short -- "$d" 2>/dev/null || true)"
	if [[ -n "$last" ]]; then
		untouched+="$last  $d"$'\n'
	fi
done
or_none "$(printf '%s' "$untouched" | sort | awk 'NR <= 15')"

# Last before status: the largest section, cut per Issue so the status block
# still fits in a tool's output window.
gh_section "open Issue bodies (800 characters each)" issue list --state open --limit 1000 \
	--json number,title,body \
	--jq '.[] | .number as $n | "--- #\($n) \(.title) ---\n\((.body // "") | if length > 800 then .[:800] + " [cut; gh issue view \($n) for the rest]" else . end)\n"'

section "status"
if ((use_github == 0)); then
	echo "github: skipped"
	echo "complete: no (source E skipped)"
	exit 0
fi
if ((github_failed)); then
	echo "github: failed"
	echo "complete: no (source E unavailable; mark the run incomplete)"
	exit 1
fi
echo "github: ok"
echo "complete: yes"
