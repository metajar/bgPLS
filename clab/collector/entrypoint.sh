#!/bin/sh
set -eu

addrs=${COLLECTOR_ADDRS:-"eth1=10.0.1.1/30,eth2=10.0.2.1/30"}
config=${BGPLS_CONFIG:-/etc/bgpls/bgpls.yaml}

wait_for_iface() {
  iface=$1
  i=0
  while [ "$i" -lt 60 ]; do
    if ip link show "$iface" >/dev/null 2>&1; then
      return 0
    fi
    i=$((i + 1))
    sleep 1
  done
  echo "interface $iface did not appear" >&2
  return 1
}

old_ifs=$IFS
IFS=,
for spec in $addrs; do
  [ -n "$spec" ] || continue
  iface=${spec%%=*}
  cidr=${spec#*=}
  wait_for_iface "$iface"
  ip link set "$iface" up
  ip addr add "$cidr" dev "$iface" 2>/dev/null || true
done
IFS=$old_ifs

exec /usr/local/bin/bgpls serve --config "$config"
