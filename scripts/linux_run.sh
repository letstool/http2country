#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p ./db

# LICENSE_KEY: your CDN license token (optional — anonymous if unset).
# COUNTRY_DB_URL:  set to another http2country instance to enable peer sync mode instead of CDN.

LISTEN_ADDR=127.0.0.1:8080 \
COUNTRY_DB_DIR=./db \
LICENSE_KEY="${LICENSE_KEY:-}" \
./out/http2country
