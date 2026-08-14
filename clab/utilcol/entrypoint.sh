#!/bin/sh
set -eu
exec /usr/local/bin/utilcol --config "${UTILCOL_CONFIG:-/etc/bgpls/utilcol.yaml}"
