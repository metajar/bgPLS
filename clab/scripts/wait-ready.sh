#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
CLI="$ROOT/clab/scripts/bgpls.sh"
TIMEOUT=${CLAB_WAIT_TIMEOUT:-300}
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

json_uint() {
  # protojson encodes uint64 as a quoted string: "node_count": "8"
  printf '%s\n' "$1" | awk -v key="$2" '
    $0 ~ "\"" key "\"" {
      gsub(/[^0-9]/, "", $2)
      if ($2 != "") { print $2; exit }
    }
  '
}

echo "API is healthy, waiting for BGP-LS sessions and topology"
while (( SECONDS < deadline )); do
  peers=$("$CLI" peers list 2>/dev/null || true)
  summary=$("$CLI" topology summary 2>/dev/null || true)
  established=$(printf '%s\n' "$peers" | grep -c 'PEER_SESSION_STATE_ESTABLISHED' || true)
  nodes=$(json_uint "$summary" "node_count")
  established=${established:-0}
  nodes=${nodes:-0}
  if (( established >= 1 && nodes >= 10 )); then
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
echo "--- FRR BGP-LS producers ---" >&2
for n in r1 r2; do
  echo "-- ${n} isis neighbors --" >&2
  docker exec "clab-bgpls-${n}" vtysh -c "show isis neighbor" >&2 || true
  echo "-- ${n} bgp summary --" >&2
  docker exec "clab-bgpls-${n}" vtysh -c "show bgp summary" >&2 || true
  echo "-- ${n} bgp link-state --" >&2
  docker exec "clab-bgpls-${n}" vtysh -c "show bgp link-state link-state" >&2 || true
  echo "-- ${n} mpls-te database --" >&2
  docker exec "clab-bgpls-${n}" vtysh -c "show isis mpls-te database" >&2 || true
done
echo "--- FRR edge to SR Linux ---" >&2
for n in r7 r8; do
  echo "-- ${n} isis neighbors --" >&2
  docker exec "clab-bgpls-${n}" vtysh -c "show isis neighbor" >&2 || true
  echo "-- ${n} isis database --" >&2
  docker exec "clab-bgpls-${n}" vtysh -c "show isis database" >&2 || true
done
echo "--- SR Linux IS-IS ---" >&2
for n in srl1 srl2; do
  echo "-- ${n} isis adjacency --" >&2
  docker exec "clab-bgpls-${n}" sr_cli -c "show network-instance default protocols isis adjacency" >&2 || true
done
echo "hint: make clab-status" >&2
exit 1
