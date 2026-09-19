#!/usr/bin/env bash
# Aggregates the ds_acquire / ds_token_invalid events emitted by ds2api into
# the two numbers the baseline decision gate actually asks for: the PEAK
# requests-per-hour reached by any single account, and every suspension event.
#
# Usage:  ./scripts/ds2api-baseline.sh [container] [since]
#   ./scripts/ds2api-baseline.sh lohari-ds2api 168h
set -euo pipefail

CONTAINER="${1:-lohari-ds2api}"
SINCE="${2:-168h}"
THRESHOLD="${DS2API_BASELINE_THRESHOLD:-20}"

logs="$(docker logs --since "$SINCE" "$CONTAINER" 2>&1)"

echo "== Peak requests per hour, per account (window: $SINCE) =="
peak="$(printf '%s\n' "$logs" \
  | grep -F '"msg":"ds_acquire"' \
  | jq -r '"\(.time[0:13]) \(.account)"' \
  | sort | uniq -c | sort -rn)"

if [ -z "$peak" ]; then
  echo "  no ds_acquire events found -- is the instrumented build deployed?"
  exit 1
fi

printf '%s\n' "$peak" | head -20 | awk '{printf "  %5d req in hour %s  %s\n", $1, $2, $3}'

max="$(printf '%s\n' "$peak" | head -1 | awk '{print $1}')"
echo
echo "== Suspension events =="
printf '%s\n' "$logs" | grep -F '"msg":"ds_token_invalid"' \
  | jq -r '"  \(.time)  \(.account)  \(.reason)"' || echo "  none"

echo
echo "== Decision gate =="
echo "  peak = $max req/hour   threshold = $THRESHOLD"
if [ "$max" -gt "$THRESHOLD" ]; then
  echo "  ABOVE threshold -> request volume is plausibly the driver. Build the budget (#7)."
else
  echo "  BELOW threshold -> volume is not the driver. Do NOT build the budget;"
  echo "  promote the client fingerprint work (#4) instead."
fi
