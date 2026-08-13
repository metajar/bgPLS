#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
PKI="$ROOT/clab/pki"
BIN="$ROOT/bgpls"
ROLE="${BGPLS_ROLE:-admin}"
SERVER="${BGPLS_SERVER:-https://127.0.0.1:7443}"
CONTAINER="${BGPLS_CONTAINER:-clab-bgpls-collector}"

usage() {
  echo "usage: $0 <topology|path|history|peers|health|version> [args...]" >&2
  echo "Set BGPLS_ROLE=admin|operator|reader (default admin)." >&2
  echo "Set BGPLS_SERVER to reach a non-local API." >&2
  exit 2
}

[[ $# -ge 1 ]] || usage

cmd=$1
shift || true

client_flags=(--server "$SERVER" --ca "$PKI/ca.crt" --cert "$PKI/${ROLE}.crt" --key "$PKI/${ROLE}.key")
container_flags=(--server https://127.0.0.1:7443 --ca /etc/bgpls/pki/ca.crt --cert "/etc/bgpls/pki/${ROLE}.crt" --key "/etc/bgpls/pki/${ROLE}.key")

if [[ "$cmd" == "version" ]]; then
  client_flags=()
  container_flags=()
fi

if [[ -x "$BIN" ]]; then
  if [[ ${#client_flags[@]} -eq 0 ]]; then
    exec "$BIN" "$cmd" "$@"
  fi
  exec "$BIN" "$cmd" "$@" "${client_flags[@]}"
fi

if command -v docker >/dev/null 2>&1 && docker inspect "$CONTAINER" >/dev/null 2>&1; then
  if [[ ${#container_flags[@]} -eq 0 ]]; then
    exec docker exec "$CONTAINER" bgpls "$cmd" "$@"
  fi
  exec docker exec "$CONTAINER" bgpls "$cmd" "$@" "${container_flags[@]}"
fi

echo "bgpls CLI not found. Build the collector image or copy ./bgpls into the repo root." >&2
exit 1
