# CLAUDE.md — http2country

This file provides context for AI-assisted development on the `http2country` project.

---

## Project overview

`http2country` is a single-binary HTTP gateway that exposes country intelligence data as a JSON REST API.
It is written entirely in Go and embeds all static assets (web UI, favicon, OpenAPI spec) at compile
time using `//go:embed` directives, so the resulting binary has zero runtime file dependencies.

The server accepts `POST /api/v1/country` requests containing a country identifier (ISO 3166-1 alpha-2,
alpha-3, or numeric code, case-insensitive) and returns full structured country data. The database is
**built automatically** from a gzipped CSV fetched from the **letstool CDN**
(`https://cdn.letstool.net/country/csv`) and refreshed every 24 hours.
An optional `LICENSE_KEY` enables licensed (higher-quota) CDN access.

---

## Repository layout

```
.
├── api/
│   └── swagger.yaml              # OpenAPI 3.1 source (human-editable)
├── build/
│   └── Dockerfile                # Two-stage Docker build (builder + scratch runtime)
├── cmd/
│   └── http2country/
│       ├── main.go               # Entire application — single file
│       └── static/
│           ├── favicon.png       # Embedded at build time
│           ├── index.html        # Embedded web UI (dark/light, 15 languages, RTL support)
│           └── openapi.json      # Embedded OpenAPI spec (generated from swagger.yaml)
├── scripts/
│   ├── 000_init.sh               # go mod tidy
│   ├── 999_test.sh               # Integration smoke tests (curl + jq)
│   ├── linux_build.sh            # Native static binary build
│   ├── linux_run.sh              # Run binary on Linux
│   ├── docker_build.sh           # Build Docker image
│   ├── docker_run.sh             # Run Docker container
│   ├── windows_build.cmd         # Native build on Windows
│   └── windows_run.cmd           # Run binary on Windows
├── go.mod
├── go.sum
├── LICENSE                       # MIT
├── README.md
└── CLAUDE.md                     # This file
```

---

## Key design decisions

- **Single `main.go`**: all server logic lives in `cmd/http2country/main.go`. There are no internal packages.
- **Embedded assets**: `favicon.png`, `index.html`, and `openapi.json` are embedded with `//go:embed`. Any change to these files is picked up at the next `go build`.
- **Static binary**: the build uses `CGO_ENABLED=0` and `-ldflags "-extldflags -static"`. Do not introduce `cgo` dependencies.
- **No framework**: the HTTP layer uses only the standard library (`net/http`). Do not add a router or web framework.
- **Custom mmdb**: the server builds its own MaxMind-compatible mmdb using `github.com/maxmind/mmdbwriter` with a custom `http2country-CountryDB` schema. Reading is done via `github.com/oschwald/maxminddb-golang` directly. **Important**: in `mmdbwriter v1.0.0` the insertion method is `writer.Insert(network, record)` — not `InsertNetwork` (which does not exist in this version).
- **IPv6 key encoding**: country records are stored in the mmdb using a synthetic /128 IPv6 address derived from the ISO 3166-1 alpha-2 code:
  - Bytes 0–9: fixed ULA prefix `fd:c0:db:00:00:00:00:00:00:00`
  - Bytes 10–11: ISO2[0] and ISO2[1] as raw ASCII bytes
  - Bytes 12–15: zeros
  - Example: `FR` → `fdc0:db00:0000:0000:0000:4652:0000:0000/128`
- **Two update modes** controlled by `COUNTRY_DB_URL`:
  - **CDN CSV mode** (default): `buildCountryDBFromCSV` fetches a gzipped CSV, decompresses it via `compress/gzip`, parses it with `encoding/csv`, and compiles a fresh `country.mmdb` via `mmdbwriter`.
  - **Peer mode** (`COUNTRY_DB_URL` set): `downloadFromPeer` downloads `country.mmdb` directly from the `/db/country` endpoint of another `http2country` instance.
- **CDN protocol**: `fetchCSVFromCDN` sends `If-Modified-Since` on every request. Status codes handled:
  - **304 Not Modified** — no quota cost; returns sentinel `errNotModified`.
  - **429 Too Many Requests** — returns `*errRateLimited` with `Retry-After` timestamp.
  - **410 Gone** — returns `*errProductGone`; retried at 24h/48h/72h/96h, then goroutine exits.
  - **401 Unauthorized** — returns `*errUnauthorized`; goroutine exits permanently.
  - **200 OK** — `Last-Modified` stored in `.last_modified_country`; CSV stream returned.
- **Hot database swap**: `*dbState` (containing `*maxminddb.Reader` + lookup maps) stored in `sync/atomic.Value`. `swapDB()` atomically replaces the state without interrupting in-flight requests.
- **Lookup normalization**: `normalizeToISO2(query, state)` accepts ISO2 (2-char), ISO3 (3-letter), or numeric codes. Input is trimmed and uppercased. Numeric codes map via `numericToISO2`; ISO3 via `iso3ToISO2`.
- **Marker files** in `COUNTRY_DB_DIR`:
  - `.last_update_country` — Unix timestamp of last successful build/download.
  - `.last_modified_country` — `Last-Modified` header from last CDN 200 response.
- **HTTP proxy support**: all outbound clients use `http.ProxyFromEnvironment`. Proxy URL logged at startup by `logProxyConfig()`; passwords redacted to `***`.
- **`/db/country` endpoint**: serves the current `tor.mmdb` file — used by peer instances in peer mode.

---

## Environment variables & CLI flags

| Environment variable | CLI flag       | Default          | Description |
|----------------------|----------------|------------------|-------------|
| `LISTEN_ADDR`        | `-listen-addr` | `127.0.0.1:8080` | HTTP server listen address |
| `COUNTRY_DB_DIR`     | `-db-dir`      | `/data`          | Directory to store `country.mmdb` |
| `COUNTRY_DB_URL`     | `-db-url`      | *(none)*         | Peer instance base URL (enables peer mode) |
| `LICENSE_KEY`        | `-license-key` | *(none)*         | CDN license token (`Authorization: Basic <token>`) |

Proxy: `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY` (and lowercase variants) via `http.ProxyFromEnvironment`.

---

## CSV source format (41 columns)

`iso2, iso3, numeric_code, fifa, status, independent, un_member, name_common, name_official, region, subregion, capital, latitude, longitude, area_sq_km, landlocked, borders, language_codes, currency_codes, tld, calling_codes, demonym_eng_m, demonym_eng_f, gini_year, gini_value, gdp_current_usd, gdp_per_capita_usd, gdp_year, flag_emoji, legal_eu, legal_eea, legal_schengen, legal_gdpr, legal_data_residency, legal_vat_rate, legal_vat_name, legal_digital_vat, legal_ofac, legal_eu_sanction, legal_un_sanction, legal_fatf`

- Multi-value fields (`borders`, `language_codes`, `currency_codes`, `tld`, `calling_codes`) use `;` as separator.
- `legal_fatf`: empty = no concern, `"grey-list"` or `"black-list"`.

---

## Data model (`CountryRecord` — 38 mmdb fields)

`iso2`, `iso3`, `numeric_code`, `fifa`, `status`, `independent` (bool), `un_member` (bool), `name_common`, `name_official`, `region`, `subregion`, `capital`, `latitude` (float64), `longitude` (float64), `area_sq_km` (float64), `landlocked` (bool), `borders` ([]string), `language_codes` ([]string), `currency_codes` ([]string), `tld` ([]string), `calling_codes` ([]string), `demonym_eng_m`, `demonym_eng_f`, `gini_year`, `gini_value` (float64), `gdp_current_usd` (float64), `gdp_per_capita_usd` (float64), `gdp_year`, `flag_emoji`, `legal_eu` (bool), `legal_eea` (bool), `legal_schengen` (bool), `legal_gdpr` (bool), `legal_data_residency` (bool), `legal_vat_rate` (float64), `legal_vat_name`, `legal_digital_vat` (bool), `legal_ofac` (bool), `legal_eu_sanction` (bool), `legal_un_sanction` (bool), `legal_fatf`.

---

## API contract

```
POST /api/v1/country          — query a country by ISO2/ISO3/numeric code
GET  /db/country              — download current country.mmdb (for peer mode)
GET  /openapi.json            — OpenAPI 3.1 spec
GET  /favicon.png             — app icon
GET  /                        — web UI
```

Response status values: `SUCCESS`, `NOTFOUND`, `ERROR`.

---

## Web UI

The UI is a self-contained single-file HTML/JS/CSS application embedded in the binary.

- **Themes**: dark and light, switchable via a toggle button. Dark theme uses a cyan-blue accent (`#00d4ff`) matching the letstool project palette.
- **Languages**: 15 locales built in — Arabic (`ar`), Bengali (`bn`), German (`de`), English (`en`), Spanish (`es`), French (`fr`), Hindi (`hi`), Indonesian (`id`), Japanese (`ja`), Korean (`ko`), Portuguese (`pt-BR`), Russian (`ru`), Urdu (`ur`), Vietnamese (`vi`), Chinese (`zh-CN`). Language is auto-detected from `navigator.languages` and selectable via dropdown.
- **RTL support**: Arabic (`ar`) and Urdu (`ur`) automatically switch the layout to right-to-left via `[dir="rtl"]` CSS rules (header alignment, table text direction, arrow positions, skip link placement, tab arrow keys).
- The UI calls `POST /api/v1/country` and renders results in a table.
- Copy-to-CSV button exports all results in pipe-delimited format.

To modify the UI, edit `cmd/http2tor/static/index.html` and rebuild.
To update the API spec, edit `api/swagger.yaml` and update `openapi.json` accordingly.

---

## Constraints & conventions

- Go version: **1.24+**
- No `cgo`. No HTTP frameworks. All logic in `cmd/http2country/main.go`.
- Error responses always return `CountryResponse` JSON — never plain text.
- Server never logs request bodies.
- All code and docs in **English**.
- Every env var has a corresponding CLI flag (flag wins).

---

## Build & run

```bash
bash scripts/000_init.sh        # tidy dependencies
bash scripts/linux_build.sh     # build -> ./out/http2country
bash scripts/linux_run.sh       # run
bash scripts/docker_build.sh    # Docker image -> letstool/http2country:latest
bash scripts/docker_run.sh      # run container
bash scripts/999_test.sh        # smoke tests
```

---

## AI-assisted development

This project was developed with the assistance of **Claude Sonnet 4.6** by Anthropic.
