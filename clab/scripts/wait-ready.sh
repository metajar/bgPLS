#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
CLI="$ROOT/clab/scripts/bgpls.sh"
TIMEOUT=${CLAB_WAIT_TIMEOUT:-180}
CONTAINER=${BGPLS_CONTAINER:-clab-bgpls-collector}

echo "waiting up to ${TIMEOUT}s for the collector API and BGP-LS topology"

deadline=$((SECONDS + TIMEOUT))
health_ok=0
while (( SECONDS < deadline )); do
  if docker inspect "$CONTAINER" >/dev/null 2>&1; then
    if "$CLI" health >/dev/null 2>&1; then
      health_ok=1
      break
    fi
  fi
  sleep 3
done

if [[ "$health_ok" -ne 1 ]]; then
  echo "collector API did not become healthy" >&2
  docker ps --filter "name=clab-bgpls-" || true
  exit 1
fi

echo "API is healthy, waiting for BGP-LS sessions and topology"
while (( SECONDS < deadline )); do
  peers=$("$CLI" peers list 2>/dev/null || true)
  summary=$("$CLI" topology summary 2>/dev/null || true)
  established=$(printf '%s\n' "$peers" | grep -c 'PEER_SESSION_STATE_ESTABLISHED' || true)
  nodes=$(printf '%s\n' "$summary" | awk -F: '/"node_count"/{gsub(/[ ,]/,"",$2); print $2; exit}')
  nodes=${nodes:-0}
  if [[ "$established" -ge 1 && "$nodes" -ge 8 ]]; then
    echo
    echo "lab is ready"
    printf '%s\n' "$summary"
    exit 0
  fi
  echo "  established peers=${established} nodes=${nodes}"
  sleep 5
done

echo "timed out waiting for BGP-LS topology" >&2
echo "--- peers ---" >&2
"$CLI" peers list >&2 || true
echo "--- summary ---" >&2
"$CLI" topology summary >&2 || true
echo "hint: make clab-status" >&2
exit 1
