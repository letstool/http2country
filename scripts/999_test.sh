#!/usr/bin/env bash
set -euo pipefail
BASE="${1:-http://localhost:8080}"
echo "=== http2country smoke tests against $BASE ==="

echo "--- [1] Lookup by ISO2 (France) ---"
curl -sf -X POST "$BASE/api/v1/country" \
  -H "Content-Type: application/json" \
  -d '{"country":"FR"}' | jq .

echo "--- [2] Lookup by ISO3 (Germany) ---"
curl -sf -X POST "$BASE/api/v1/country" \
  -H "Content-Type: application/json" \
  -d '{"country":"DEU"}' | jq .

echo "--- [3] Lookup by numeric code (Japan) ---"
curl -sf -X POST "$BASE/api/v1/country" \
  -H "Content-Type: application/json" \
  -d '{"country":"392"}' | jq .

echo "--- [4] Lowercase ISO2 (Spain) ---"
curl -sf -X POST "$BASE/api/v1/country" \
  -H "Content-Type: application/json" \
  -d '{"country":"es"}' | jq .

echo "--- [5] Unknown country code ---"
curl -sf -X POST "$BASE/api/v1/country" \
  -H "Content-Type: application/json" \
  -d '{"country":"XX"}' | jq .

echo "--- [6] GET /openapi.json ---"
curl -sf "$BASE/openapi.json" | jq .info.title

echo "--- [7] GET /db/country (header only) ---"
curl -sf -I "$BASE/db/country" | head -3

echo "=== All tests complete ==="
