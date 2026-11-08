#!/usr/bin/env bash
#
# folder-agent-demo-setup.sh — prepare a running sandbox demo for the folder
# agent: onboarding skipped, the assistant hired, Personal HQ created, the
# system model set, and saved folder agents ("<folder> Manager") on a chosen
# provider so Start binds them by name.
#
# Usage:
#   ./scripts/folder-agent-demo-setup.sh URL [--provider claude] [--model claude-sonnet-5-5] [FOLDER_NAME ...]
#
# Run it only against an isolated demo server (wt demo, scripts/demo-server.sh);
# it hires an assistant and creates agents. With no provider keys in the
# server's environment the agents simply have no model (the no-model path).
set -euo pipefail

url="${1:-}"
if [[ -z "$url" || "$url" != http://localhost:* ]]; then
	echo "usage: $0 http://localhost:PORT [--provider P] [--model M] [FOLDER_NAME ...]" >&2
	exit 2
fi
shift
provider="claude"
model="claude-sonnet-5-5"
folders=()
while (($#)); do
	case "$1" in
		--provider) provider="$2"; shift 2 ;;
		--model) model="$2"; shift 2 ;;
		*) folders+=("$1"); shift ;;
	esac
done

json() { python3 -I -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
post() { curl -sS -X POST "$url$1" -H 'Content-Type: application/json' -d "$2"; }

post /api/onboarding/skip '{}' >/dev/null
version="$(curl -sS "$url/api/personal-assistant" | json 'd["personal_assistant"].get("state_version", 0)')"
if [[ "$(curl -sS "$url/api/personal-assistant" | json 'd["personal_assistant"].get("hired", False)')" != "True" ]]; then
	version="$(post /api/personal-assistant/hire "{\"request_id\":\"fa-demo-hire\",\"if_version\":$version,\"display_name\":\"Atlas\",\"mandate\":\"Help me with my songs.\",\"focus_areas\":[\"plan_my_day\"]}" |
		json 'd["personal_assistant"]["state_version"]')"
fi
post /api/personal-assistant/hq "{\"request_id\":\"fa-demo-hq\",\"if_version\":$version,\"name\":\"My HQ\",\"timezone\":\"UTC\"}" >/dev/null || true
post /api/settings/system-model "{\"provider\":\"$provider\",\"model\":\"$model\"}" >/dev/null || true
for folder in ${folders[@]+"${folders[@]}"}; do
	code="$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$url/api/agents" -H 'Content-Type: application/json' \
		-d "{\"name\":\"$folder Manager\",\"role\":\"orchestrator\",\"model\":\"$model\",\"llm_provider\":\"$provider\"}")"
	echo "agent \"$folder Manager\" ($provider/$model): $code"
done
echo "ready: $url"
