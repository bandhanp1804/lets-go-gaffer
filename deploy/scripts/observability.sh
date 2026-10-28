#!/usr/bin/env bash
# Publishes the Datadog dashboards, monitors and SLO in deploy/observability. Monitors notify the
# address in DD_NOTIFY by email (and the Datadog mobile app, when it is the address you sign in to
# Datadog with). Run from anywhere; the Deploy workflow runs it after the app.
#
#   deploy/scripts/observability.sh
#
# Safe to rerun: each item is found by its title or name and updated in place, so edits belong in
# the repository, not in Datadog's UI (which this overwrites). Needs DD_API_KEY and DD_APP_KEY (from
# the environment, as in CI, or deploy/azure.env); without them it does nothing.
set -euo pipefail
cd "$(dirname "$0")/../.."

DIR=deploy/observability
API=${DD_API_URL:-https://api.us5.datadoghq.com}

# envvar KEY FILE: the value of KEY from the environment (as in CI), else from FILE, never echoed.
envvar() {
  if [ -n "${!1:-}" ]; then printf '%s' "${!1}"; return; fi
  grep -E "^$1=" "$2" 2>/dev/null | head -1 | cut -d= -f2- || true
}

apikey=$(envvar DD_API_KEY deploy/azure.env)
appkey=$(envvar DD_APP_KEY deploy/azure.env)
if [ -z "$apikey" ] || [ -z "$appkey" ]; then
  echo "observability: DD_API_KEY or DD_APP_KEY is not set; nothing published"
  exit 0
fi

# The keys go to curl in a file only this user can read, never on a command line.
headers=$(mktemp)
trap 'rm -f "$headers"' EXIT
chmod 600 "$headers"
printf 'DD-API-KEY: %s\nDD-APPLICATION-KEY: %s\nContent-Type: application/json\n' "$apikey" "$appkey" >"$headers"

# dd METHOD PATH [BODY_FILE]: one Datadog API call; fails loudly with Datadog's message.
dd() {
  local out code
  out=$(mktemp)
  code=$(curl -sS -o "$out" -w '%{http_code}' -X "$1" "$API$2" -H @"$headers" ${3:+--data @"$3"})
  if [ "${code:0:1}" != 2 ]; then
    echo "observability: $1 $2 answered $code: $(head -c 400 "$out")" >&2
    rm -f "$out"
    return 1
  fi
  cat "$out"
  rm -f "$out"
}

# Who the monitors notify: an email address (the Deploy workflow passes the budget alert address).
notify=$(envvar DD_NOTIFY deploy/azure.env)
[ -n "$notify" ] && mention="@$notify" || mention=""
[ -n "$notify" ] || echo "no DD_NOTIFY: monitors will alert inside Datadog only"

echo "== dashboards"
existing=$(dd GET /api/v1/dashboard)
for f in "$DIR"/dashboards/*.json; do
  title=$(jq -r .title "$f")
  id=$(jq -r --arg t "$title" '[.dashboards[] | select(.title == $t) | .id][0] // empty' <<<"$existing")
  if [ -n "$id" ]; then
    dd PUT "/api/v1/dashboard/$id" "$f" >/dev/null
  else
    id=$(dd POST /api/v1/dashboard "$f" | jq -r .id)
  fi
  echo "$title: https://${API#https://api.}/dashboard/$id"
done

echo "== SLO"
name=$(jq -r .name "$DIR/slo.json")
slo=$(dd GET "/api/v1/slo?query=$(jq -rn --arg n "$name" '$n | @uri')" |
  jq -r --arg n "$name" '[.data[]? | select(.name == $n) | .id][0] // empty')
if [ -n "$slo" ]; then
  dd PUT "/api/v1/slo/$slo" "$DIR/slo.json" >/dev/null
else
  slo=$(dd POST /api/v1/slo "$DIR/slo.json" | jq -r '.data[0].id')
fi
echo "$name: $slo"

echo "== monitors"
count=$(jq length "$DIR/monitors.json")
for i in $(seq 0 $((count - 1))); do
  m=$(mktemp)
  jq --arg slo "$slo" --arg to "$mention" \
    ".[$i] | .query |= gsub(\"__SLO_ID__\"; \$slo) | .message |= gsub(\"__NOTIFY__\"; \$to)" "$DIR/monitors.json" >"$m"
  name=$(jq -r .name "$m")
  id=$(dd GET "/api/v1/monitor?name=$(jq -rn --arg n "$name" '$n | @uri')" |
    jq -r --arg n "$name" '[.[] | select(.name == $n) | .id][0] // empty')
  if [ -n "$id" ]; then
    dd PUT "/api/v1/monitor/$id" "$m" >/dev/null
  else
    id=$(dd POST /api/v1/monitor "$m" | jq -r .id)
  fi
  rm -f "$m"
  echo "$name: $id"
done
