#!/bin/sh
set -eu
mkdir -p /var/lib/net-snmp /run/snmpd /var/run/snmpd 2>/dev/null || true
if command -v snmpd >/dev/null 2>&1; then
  snmpd -c /etc/snmp/snmpd.conf -Le || true
fi
if [ -x /usr/lib/frr/docker-start ]; then
  exec /usr/lib/frr/docker-start "$@"
fi
exec /sbin/tini -- /usr/lib/frr/docker-start "$@"
