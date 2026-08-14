#!/bin/sh
set -eu
mkdir -p /var/lib/net-snmp /run/snmpd /var/run/snmpd 2>/dev/null || true
if command -v snmpd >/dev/null 2>&1; then
  # -C ignores compiled-in/default endpoints. Alpine net-snmp fails to bind
  # the implicit "udp:161" even when snmpd.conf specifies 0.0.0.0:161.
  snmpd -C -c /etc/snmp/snmpd.conf -p /run/snmpd.pid -Ls d || true
fi
if [ -x /usr/lib/frr/docker-start ]; then
  exec /usr/lib/frr/docker-start "$@"
fi
exec /sbin/tini -- /usr/lib/frr/docker-start "$@"
