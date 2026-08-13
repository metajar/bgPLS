#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
PKI="$ROOT/clab/pki"
mkdir -p "$PKI"

detect_host_ip() {
  if [[ -n "${CLAB_HOST_IP:-}" ]]; then
    printf '%s\n' "$CLAB_HOST_IP"
    return
  fi
  if command -v ip >/dev/null 2>&1; then
    ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i <= NF; i++) if ($i == "src") { print $(i + 1); exit }}'
    return
  fi
  if command -v ipconfig >/dev/null 2>&1; then
    ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null || true
    return
  fi
}

HOST_IP=$(detect_host_ip || true)
[[ -n "$HOST_IP" ]] || HOST_IP=127.0.0.1

SAN_DNS=$'DNS.1 = localhost\nDNS.2 = collector\nDNS.3 = bgpls\nDNS.4 = bgpls.lab\nDNS.5 = clab-bgpls-collector'
SAN_IP=$'IP.1 = 127.0.0.1\nIP.2 = ::1'
idx=3
if [[ "$HOST_IP" != "127.0.0.1" ]]; then
  SAN_IP+=$'\n'"IP.${idx} = ${HOST_IP}"
  idx=$((idx + 1))
fi
if [[ -n "${CLAB_EXTRA_IPS:-}" ]]; then
  IFS=',' read -r -a extras <<<"$CLAB_EXTRA_IPS"
  for extra in "${extras[@]}"; do
    extra=${extra// /}
    [[ -n "$extra" ]] || continue
    SAN_IP+=$'\n'"IP.${idx} = ${extra}"
    idx=$((idx + 1))
  done
fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

cat >"$TMP/ca.cnf" <<'EOF'
[req]
distinguished_name = dn
x509_extensions = ext
prompt = no
[dn]
CN = bgPLS Lab CA
[ext]
basicConstraints = critical,CA:TRUE
keyUsage = critical,keyCertSign,cRLSign
subjectKeyIdentifier = hash
EOF

openssl req -x509 -newkey rsa:2048 -sha256 -days 3650 -nodes \
  -keyout "$PKI/ca.key" -out "$PKI/ca.crt" \
  -config "$TMP/ca.cnf" >/dev/null 2>&1

cat >"$TMP/server.cnf" <<EOF
[req]
distinguished_name = dn
req_extensions = ext
prompt = no
[dn]
CN = bgpls
[ext]
basicConstraints = CA:FALSE
keyUsage = critical,digitalSignature,keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @san
[san]
${SAN_DNS}
${SAN_IP}
EOF

openssl req -newkey rsa:2048 -sha256 -nodes \
  -keyout "$PKI/server.key" -out "$TMP/server.csr" \
  -config "$TMP/server.cnf" >/dev/null 2>&1
openssl x509 -req -in "$TMP/server.csr" -CA "$PKI/ca.crt" -CAkey "$PKI/ca.key" \
  -CAcreateserial -out "$PKI/server.crt" -days 825 -sha256 \
  -extfile "$TMP/server.cnf" -extensions ext >/dev/null 2>&1

issue_client() {
  local role=$1
  cat >"$TMP/${role}.cnf" <<EOF
[req]
distinguished_name = dn
req_extensions = ext
prompt = no
[dn]
CN = ${role}
[ext]
basicConstraints = CA:FALSE
keyUsage = critical,digitalSignature,keyEncipherment
extendedKeyUsage = clientAuth
subjectAltName = @san
[san]
URI.1 = spiffe://bgpls.lab/bgpls/${role}/cli
DNS.1 = ${role}.bgpls.lab
EOF
  openssl req -newkey rsa:2048 -sha256 -nodes \
    -keyout "$PKI/${role}.key" -out "$TMP/${role}.csr" \
    -config "$TMP/${role}.cnf" >/dev/null 2>&1
  openssl x509 -req -in "$TMP/${role}.csr" -CA "$PKI/ca.crt" -CAkey "$PKI/ca.key" \
    -CAcreateserial -out "$PKI/${role}.crt" -days 825 -sha256 \
    -extfile "$TMP/${role}.cnf" -extensions ext >/dev/null 2>&1
  openssl pkcs12 -export -inkey "$PKI/${role}.key" -in "$PKI/${role}.crt" \
    -certfile "$PKI/ca.crt" -out "$PKI/${role}.p12" -passout pass:bgpls >/dev/null 2>&1
}

issue_client admin
issue_client operator
issue_client reader

chmod 600 "$PKI"/*.key
rm -f "$PKI/ca.srl" "$PKI/.srl" 2>/dev/null || true
printf '%s\n' "$HOST_IP" >"$PKI/host-ip"
echo "lab PKI written to $PKI (server SAN includes ${HOST_IP})"
