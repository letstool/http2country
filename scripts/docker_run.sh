#!/usr/bin/env bash
set -euo pipefail

# Mode 1 (default): fetch gzipped CSV from the CDN and compile country.mmdb.
# Set LICENSE_KEY to your token for licensed (higher quota) access.
docker run -it --rm \
  -p 8080:8080 \
  -v "$(pwd)/db:/data:rw" \
  -e LISTEN_ADDR=0.0.0.0:8080 \
  -e LICENSE_KEY="${LICENSE_KEY:-}" \
  letstool/http2country:latest

# Mode 2 (peer): download country.mmdb from another running http2country instance.
# Uncomment and set COUNTRY_DB_URL to use this mode:
#
# docker run -it --rm \
#   -p 8080:8080 \
#   -v "$(pwd)/db:/data:rw" \
#   -e LISTEN_ADDR=0.0.0.0:8080 \
#   -e COUNTRY_DB_URL=http://upstream-host:8080 \
#   letstool/http2country:latest
