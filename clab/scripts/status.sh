#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
CLI="$ROOT/clab/scripts/bgpls.sh"
PREFIX=${CLAB_PREFIX:-clab-bgpls}

echo "== containerlab nodes =="
if command -v "${CONTAINERLAB:-containerlab}" >/dev/null 2>&1; then
  "${CONTAINERLAB:-containerlab}" inspect -t "$ROOT/clab/bgpls.clab.yml" || true
fi

echo
echo "== collector API =="
"$CLI" health || true
"$CLI" peers list || true
"$CLI" topology summary || true

echo
echo "== IS-IS neighbors =="
for n in r1 r2 r3 r4 r5 r6 r7 r8; do
  echo "-- $n --"
  docker exec "${PREFIX}-${n}" vtysh -c "show isis neighbor" 2>/dev/null || echo "unable to query $n"
done
for n in srl1 srl2; do
  echo "-- $n --"
  docker exec "${PREFIX}-${n}" sr_cli -c "show network-instance default protocols isis adjacency" 2>/dev/null || echo "unable to query $n"
done

echo
echo "== BGP-LS on producers =="
for n in r1 r2; do
  echo "-- $n bgp summary --"
  docker exec "${PREFIX}-${n}" vtysh -c "show bgp summary" 2>/dev/null || true
  echo "-- $n link-state --"
  docker exec "${PREFIX}-${n}" vtysh -c "show bgp link-state link-state" 2>/dev/null || true
done
