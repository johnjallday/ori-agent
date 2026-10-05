#!/usr/bin/env bash
# Install the reviewed herdr-remote pin as a loopback-only Telegram service.
# Keep this script compatible with the macOS system Bash (3.2).
set -uo pipefail

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(CDPATH= cd -- "$script_dir/../.." && pwd)"
source_dir="$HOME/.local/share/herdr-remote/v0.7.5"
source_url="https://github.com/dcolinmorgan/herdr-remote.git"
source_tag="v0.7.5"
expected_commit="ea5a8e2a9820e84d0ca27278b46cbb6e33045916"
installer="$source_dir/relay/install-service.sh"

fail() {
  printf 'herdr-remote setup failed: %s\n' "$1" >&2
  exit 1
}

[[ "$(uname -s)" == "Darwin" ]] || fail "this setup is supported only on macOS"
command -v git >/dev/null 2>&1 || fail "git is required"
command -v herdr >/dev/null 2>&1 || fail "herdr is required"
command -v uv >/dev/null 2>&1 || fail "uv is required"
command -v jq >/dev/null 2>&1 || fail "jq is required"
[[ -x /usr/bin/curl ]] || fail "/usr/bin/curl is required"
[[ -t 0 ]] || fail "run this installer from an interactive terminal"

if [[ ! -e "$source_dir" ]]; then
  mkdir -p "$(dirname "$source_dir")" || fail "could not create the install directory"
  printf 'Cloning reviewed herdr-remote %s...\n' "$source_tag"
  git clone --quiet --branch "$source_tag" --depth 1 "$source_url" "$source_dir" ||
    fail "could not clone the reviewed source"
fi

[[ -d "$source_dir/.git" ]] || fail "$source_dir is not a Git checkout"
actual_commit="$(git -C "$source_dir" rev-parse HEAD 2>/dev/null || true)"
[[ "$actual_commit" == "$expected_commit" ]] ||
  fail "pin mismatch at $source_dir (expected $expected_commit)"
[[ -z "$(git -C "$source_dir" status --porcelain 2>/dev/null || true)" ]] ||
  fail "the pinned source has local changes"
[[ -x "$installer" ]] || fail "the pinned service installer is missing"

printf 'Linking the bundled local event plugin from the reviewed pin...\n'
herdr plugin link "$source_dir/relay" >/dev/null || fail "could not link the bundled event plugin"

# Reuse the same gate that blocks `wt away arm`. A verified rerun is a no-op;
# an incomplete existing configuration is left untouched for explicit repair.
AWAY_DISPATCH_SOURCE_ONLY=1 source "$repo_root/scripts/away-dispatch.sh"
if away_verify_herdr_remote >/dev/null 2>&1; then
  printf '%s\n' "herdr-remote is already ready: pinned loopback relay and private Telegram service."
  exit 0
fi
if [[ -f "$away_herdr_config_file" || -f "$away_herdr_secrets_file" ]]; then
  fail "an incomplete herdr-remote configuration already exists in $HOME/.config/herdr-remote"
fi

telegram_api() {
  local method="$1"
  shift
  /usr/bin/curl -fsS --max-time 20 -X POST --config - "$@" <<EOF
url = "https://api.telegram.org/bot${telegram_token}/${method}"
EOF
}

cat <<'EOF'

Create a new bot with @BotFather if needed. Its token is entered only at the
hidden prompt below; do not paste it into chat, logs, task files, or this
repository. This wrapper forces the upstream installer to choose no Cloudflare
tunnel and will not show a Cloudflare prompt.
EOF
printf 'BotFather token: '
IFS= read -r -s telegram_token
printf '\n'
[[ "$telegram_token" =~ ^[0-9]+:[A-Za-z0-9_-]+$ ]] || fail "invalid BotFather token format"

bot_response="$(telegram_api getMe 2>/dev/null)" || fail "Telegram rejected the bot token or is unreachable"
bot_username="$(printf '%s\n' "$bot_response" | jq -er \
  'select(.ok == true) | .result.username | select(type == "string" and length > 0)' 2>/dev/null)" ||
  fail "Telegram did not return a bot username"

printf '\nOpen @%s in Telegram from your own account and send /start in a direct conversation.\n' "$bot_username"
printf 'Press Enter after sending /start... '
IFS= read -r _
updates="$(telegram_api getUpdates --data-urlencode 'timeout=10' 2>/dev/null)" ||
  fail "could not read the bot's recent Telegram updates"
choices="$(printf '%s\n' "$updates" | python3 -c '
import json, sys
payload = json.load(sys.stdin)
seen = set()
for update in reversed(payload.get("result", [])):
    message = update.get("message") or {}
    chat = message.get("chat") or {}
    chat_id = chat.get("id")
    if chat.get("type") != "private" or not isinstance(chat_id, int) or chat_id <= 0 or chat_id in seen:
        continue
    seen.add(chat_id)
    label = " ".join(str(chat.get(k, "")) for k in ("first_name", "last_name")).strip() or str(chat_id)
    label = label.replace(chr(9), " ").replace(chr(10), " ")
    print(f"{chat_id}\t{label}")
' 2>/dev/null)" || fail "could not parse Telegram updates"
[[ -n "$choices" ]] || fail "no recent private /start conversation was found"
choice_count="$(printf '%s\n' "$choices" | wc -l | tr -d ' ')"
if [[ "$choice_count" -eq 1 ]]; then
  selected="$(printf '%s\n' "$choices" | sed -n '1p')"
else
  printf '\nRecent private conversations:\n'
  choice_number=0
  while IFS=$'\t' read -r candidate_id candidate_label; do
    choice_number=$((choice_number + 1))
    printf '  %s) %s (%s)\n' "$choice_number" "$candidate_label" "$candidate_id"
  done <<< "$choices"
  printf 'Select your private account [1-%s]: ' "$choice_count"
  IFS= read -r choice_number
  [[ "$choice_number" =~ ^[0-9]+$ && "$choice_number" -ge 1 && "$choice_number" -le "$choice_count" ]] ||
    fail "invalid private-chat selection"
  selected="$(printf '%s\n' "$choices" | sed -n "${choice_number}p")"
fi
telegram_chat_id="${selected%%$'\t'*}"
[[ "$telegram_chat_id" =~ ^[1-9][0-9]*$ ]] || fail "invalid private Telegram account id"
relay_token="$(python3 -c 'import secrets; print(secrets.token_hex(32))')" ||
  fail "could not generate the relay token"

# All upstream choices are supplied here: keep the enabled private Telegram
# values, send its test message, and answer `no` to cloudflared. Credentials go
# through stdin/environment only and are persisted by upstream as mode 0600.
installer_status=0
printf '\n\n\n\nn' | \
  HERDR_INSTALL_SKIP_CLOUDFLARED=1 \
  HERDR_RELAY_TOKEN="$relay_token" \
  HERDR_TG_ENABLED=true \
  HERDR_TG_TOKEN="$telegram_token" \
  HERDR_TG_CHAT_ID="$telegram_chat_id" \
  HERDR_TG_CHAT_TYPE=private \
  HERDR_TG_USERNAME="$bot_username" \
  "$installer" || installer_status=$?
telegram_token=""
relay_token=""
if [[ "$installer_status" -ne 0 ]]; then
  printf '%s\n' "The upstream smoke test exited early; waiting for the bounded safety gate before deciding setup failed." >&2
fi

# If any pin, permission, listener, plugin, service, private-chat, or no-tunnel
# assertion fails, remove the just-installed user services rather than leaving
# a partial remote path. First launch can spend more than upstream's three-second
# smoke window creating uv's environment, so allow a bounded readiness wait.
printf '\nVerifying the exact loopback/private-Telegram chain...\n'
herdr_ready=0
attempt=0
while [[ "$attempt" -lt 30 ]]; do
  if away_verify_herdr_remote >/dev/null 2>&1; then
    herdr_ready=1
    break
  fi
  attempt=$((attempt + 1))
  sleep 0.5
done
if [[ "$herdr_ready" -ne 1 ]]; then
  away_verify_herdr_remote || true
  printf '%s\n' "Verification failed; removing herdr-remote user services (configuration is preserved)." >&2
  "$installer" --uninstall || true
  exit 1
fi

printf '%s\n' "herdr-remote is ready: pinned source, token-protected loopback relay, private Telegram service, no tunnel."
printf '%s\n' "Next: verify /agents, /read, /reply, and a completion notification from the owner's phone off-network."
