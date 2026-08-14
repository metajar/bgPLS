#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
CLI="$ROOT/clab/scripts/bgpls.sh"
TGEN1=${TGEN1:-clab-bgpls-tgen1}
TGEN2=${TGEN2:-clab-bgpls-tgen2}
DST_IP=${DST_IP:-10.10.2.2}

usage() {
  cat <<'EOF' >&2
usage: traffic.sh <steady|surge|stop|demo> [options]
  steady [--rate 200M] [--duration 0]
  surge  [--rate 900M] [--duration 60]
  stop
  demo
EOF
  exit 2
}

[[ $# -ge 1 ]] || usage
cmd=$1
shift || true

rate=200M
duration=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --rate) rate=$2; shift 2 ;;
    --duration) duration=$2; shift 2 ;;
    *) usage ;;
  esac
done

ensure_server() {
  docker exec "$TGEN2" sh -c 'pkill iperf3 >/dev/null 2>&1 || true'
  docker exec -d "$TGEN2" iperf3 -s
  sleep 1
}

stop_all() {
  docker exec "$TGEN1" sh -c 'pkill iperf3 >/dev/null 2>&1 || true' || true
  docker exec "$TGEN2" sh -c 'pkill iperf3 >/dev/null 2>&1 || true' || true
}

start_flow() {
  local bps=$1
  local t=$2
  ensure_server
  docker exec "$TGEN1" sh -c 'pkill iperf3 >/dev/null 2>&1 || true' || true
  if [[ "$t" == "0" ]]; then
    docker exec -d "$TGEN1" iperf3 -c "$DST_IP" -b "$bps" -t 86400 -P 1
  else
    docker exec -d "$TGEN1" iperf3 -c "$DST_IP" -b "$bps" -t "$t" -P 1
  fi
}

path_json() {
  local extra=("$@")
  "$CLI" path compute --domain core --source r1 --destination srl2 --metric igp "${extra[@]}"
}

demo() {
  echo "=== traffic demo: steady then surge, watching CSPF ==="
  stop_all
  start_flow 200M 0
  echo "steady 200M tgen1 -> tgen2; waiting 30s for utilization overlay"
  sleep 30
  before=$(path_json)
  constraint=$(printf '%s\n' "$before" | python3 -c '
import json, sys
data = json.load(sys.stdin)
paths = data.get("paths") or []
if not paths:
    print("0", file=sys.stderr)
    sys.exit(1)
avail = int(paths[0].get("bottleneck_available_bps") or paths[0].get("bottleneckAvailableBps") or 0)
headroom = 350_000_000
constraint = max(avail - headroom, avail // 2) if avail else 1
print(constraint)
print(f"current bottleneck available_bps={avail} min-available-bw={constraint}", file=sys.stderr)
')
  echo "=== path before surge ==="
  printf '%s\n' "$before"
  start_flow 900M 90
  echo "surge 900M; waiting 20s for overlay + CSPF"
  sleep 20
  after=$(path_json --min-available-bw "${constraint}")
  echo "=== path after surge (min-available-bw ${constraint}) ==="
  printf '%s\n' "$after"
  BEFORE_JSON=$before AFTER_JSON=$after python3 - <<'PY'
import json, os
before = json.loads(os.environ["BEFORE_JSON"])
after = json.loads(os.environ["AFTER_JSON"])

def hops(msg):
    paths = msg.get("paths") or []
    if not paths:
        return []
    out = []
    for hop in paths[0].get("hops") or []:
        node = (hop.get("node") or {}).get("name") or ((hop.get("node") or {}).get("meta") or {}).get("id")
        link = hop.get("outgoingLink") or hop.get("outgoing_link") or {}
        lid = (link.get("meta") or {}).get("id")
        if lid:
            out.append(f"{node}:{lid}")
        elif node:
            out.append(str(node))
    return out

b, a = hops(before), hops(after)
if not b:
    raise SystemExit("no path before surge")
if a == b:
    raise SystemExit(f"path did not change after surge: {a}")
if not a:
    print("after surge: no path satisfies constraints (acceptable)")
else:
    print("path changed:")
    print("  before:", " -> ".join(b))
    print("  after: ", " -> ".join(a))
PY
  echo "demo passed"
}

case "$cmd" in
  steady) start_flow "$rate" "$duration" ;;
  surge)
    [[ "$duration" == "0" ]] && duration=60
    start_flow "$rate" "$duration"
    ;;
  stop) stop_all ;;
  demo) demo ;;
  *) usage ;;
esac
