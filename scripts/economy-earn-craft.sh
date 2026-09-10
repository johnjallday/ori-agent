#!/bin/bash
# Earn Craft on a running demo server the honest way: by sending chat messages.
#
# Each message is distinct, so none of them trips the duplicate window, and the
# hourly cap still applies — this cannot mint more than the cap allows, which is
# the point. Used to reach a buildable balance for the spending checkpoint
# without seeding the ledger by hand.
#
# Usage:  ./scripts/economy-earn-craft.sh [count] [port]
set -euo pipefail

COUNT="${1:-20}"
PORT="${2:-8931}"
BASE="http://localhost:${PORT}"

for i in $(seq 1 "$COUNT"); do
  curl -s -o /dev/null -X POST "${BASE}/api/chat" \
    -H 'Content-Type: application/json' \
    -d "{\"question\":\"economy demo message number ${i}\",\"agent_name\":\"\"}"
done

printf 'economy: '
curl -s "${BASE}/api/economy"
printf '\n'
