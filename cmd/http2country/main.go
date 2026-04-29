package main

import (
	_ "embed"
	"compress/gzip"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	_ "github.com/breml/rootcerts"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
	"github.com/oschwald/maxminddb-golang"
)

//go:embed static/index.html
var indexHTML []byte

//go:embed static/favicon.png
var faviconPNG []byte

//go:embed static/openapi.json
var openapiJSON []byte

// IPv6 MMDB keyspace
// bytes  0– 9 : fd:c0:db:00:00:00:00:00:00:00 (fixed 80-bit ULA prefix)
// bytes 10–11 : ISO2[0], ISO2[1] as raw ASCII bytes
// bytes 12–15 : zeros
// mask         : /128
var ipv6Prefix = [10]byte{0xfd, 0xc0, 0xdb, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

func countryToIPv6Net(iso2 string) (*net.IPNet, error) {
	if len(iso2) != 2 {
		return nil, fmt.Errorf("invalid ISO2 code %q (must be 2 chars)", iso2)
	}
	ip := make(net.IP, 16)
	copy(ip[0:10], ipv6Prefix[:])
	ip[10] = iso2[0]
	ip[11] = iso2[1]
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)}, nil
}

/* ---------- mmdb record type ---------- */

type CountryRecord struct {
	ISO2               string   `maxminddb:"iso2"`
	ISO3               string   `maxminddb:"iso3"`
	NumericCode        string   `maxminddb:"numeric_code"`
	FIFA               string   `maxminddb:"fifa"`
	Status             string   `maxminddb:"status"`
	Independent        bool     `maxminddb:"independent"`
	UNMember           bool     `maxminddb:"un_member"`
	NameCommon         string   `maxminddb:"name_common"`
	NameOfficial       string   `maxminddb:"name_official"`
	Region             string   `maxminddb:"region"`
	Subregion          string   `maxminddb:"subregion"`
	Capital            string   `maxminddb:"capital"`
	Latitude           float64  `maxminddb:"latitude"`
	Longitude          float64  `maxminddb:"longitude"`
	AreaSqKm           float64  `maxminddb:"area_sq_km"`
	Landlocked         bool     `maxminddb:"landlocked"`
	Borders            []string `maxminddb:"borders"`
	LanguageCodes      []string `maxminddb:"language_codes"`
	CurrencyCodes      []string `maxminddb:"currency_codes"`
	TLDs               []string `maxminddb:"tld"`
	CallingCodes       []string `maxminddb:"calling_codes"`
	DemonymEngM        string   `maxminddb:"demonym_eng_m"`
	DemonymEngF        string   `maxminddb:"demonym_eng_f"`
	GINIYear           string   `maxminddb:"gini_year"`
	GINIValue          float64  `maxminddb:"gini_value"`
	GDPCurrentUSD      float64  `maxminddb:"gdp_current_usd"`
	GDPPerCapitaUSD    float64  `maxminddb:"gdp_per_capita_usd"`
	GDPYear            string   `maxminddb:"gdp_year"`
	FlagEmoji          string   `maxminddb:"flag_emoji"`
	LegalEU            bool     `maxminddb:"legal_eu"`
	LegalEEA           bool     `maxminddb:"legal_eea"`
	LegalSchengen      bool     `maxminddb:"legal_schengen"`
	LegalGDPR          bool     `maxminddb:"legal_gdpr"`
	LegalDataResidency bool     `maxminddb:"legal_data_residency"`
	LegalVATRate       float64  `maxminddb:"legal_vat_rate"`
	LegalVATName       string   `maxminddb:"legal_vat_name"`
	LegalDigitalVAT    bool     `maxminddb:"legal_digital_vat"`
	LegalOFAC          bool     `maxminddb:"legal_ofac"`
	LegalEUSanction    bool     `maxminddb:"legal_eu_sanction"`
	LegalUNSanction    bool     `maxminddb:"legal_un_sanction"`
	LegalFATF          string   `maxminddb:"legal_fatf"`
}

/* ---------- API types ---------- */

type CountryAnswer struct {
	ISO2               string   `json:"iso2"`
	ISO3               string   `json:"iso3"`
	NumericCode        string   `json:"numeric_code"`
	FIFA               string   `json:"fifa,omitempty"`
	Status             string   `json:"status"`
	Independent        bool     `json:"independent"`
	UNMember           bool     `json:"un_member"`
	NameCommon         string   `json:"name_common"`
	NameOfficial       string   `json:"name_official"`
	Region             string   `json:"region"`
	Subregion          string   `json:"subregion,omitempty"`
	Capital            string   `json:"capital,omitempty"`
	Latitude           float64  `json:"latitude"`
	Longitude          float64  `json:"longitude"`
	AreaSqKm           float64  `json:"area_sq_km"`
	Landlocked         bool     `json:"landlocked"`
	Borders            []string `json:"borders"`
	LanguageCodes      []string `json:"language_codes"`
	CurrencyCodes      []string `json:"currency_codes"`
	TLDs               []string `json:"tld"`
	CallingCodes       []string `json:"calling_codes"`
	DemonymEngM        string   `json:"demonym_eng_m,omitempty"`
	DemonymEngF        string   `json:"demonym_eng_f,omitempty"`
	GINIYear           string   `json:"gini_year,omitempty"`
	GINIValue          float64  `json:"gini_value"`
	GDPCurrentUSD      float64  `json:"gdp_current_usd"`
	GDPPerCapitaUSD    float64  `json:"gdp_per_capita_usd"`
	GDPYear            string   `json:"gdp_year,omitempty"`
	FlagEmoji          string   `json:"flag_emoji,omitempty"`
	LegalEU            bool     `json:"legal_eu"`
	LegalEEA           bool     `json:"legal_eea"`
	LegalSchengen      bool     `json:"legal_schengen"`
	LegalGDPR          bool     `json:"legal_gdpr"`
	LegalDataResidency bool     `json:"legal_data_residency"`
	LegalVATRate       float64  `json:"legal_vat_rate"`
	LegalVATName       string   `json:"legal_vat_name,omitempty"`
	LegalDigitalVAT    bool     `json:"legal_digital_vat"`
	LegalOFAC          bool     `json:"legal_ofac"`
	LegalEUSanction    bool     `json:"legal_eu_sanction"`
	LegalUNSanction    bool     `json:"legal_un_sanction"`
	LegalFATF          string   `json:"legal_fatf,omitempty"`
}

type CountryResponse struct {
	Status string         `json:"status"`
	Query  string         `json:"query"`
	Answer *CountryAnswer `json:"answer,omitempty"`
}

type CountryRequest struct {
	Country string `json:"country"`
}

/* ---------- DB state ---------- */

type dbState struct {
	reader        *maxminddb.Reader
	iso3ToISO2    map[string]string
	numericToISO2 map[string]string
}

/* ---------- Configuration ---------- */

var (
	dbDir      string
	dbURL      string
	licenseKey string
	listenAddr string
	stateValue atomic.Value
)

const (
	lastUpdateFile   = ".last_update_country"
	lastModifiedFile = ".last_modified_country"
	dbFileName       = "country.mmdb"
	cdnCSVURL        = "https://cdn.letstool.net/country/csv"
	updateInterval   = 24 * time.Hour
)

/* ---------- Helpers ---------- */

func writeTimestamp() {
	p := filepath.Join(dbDir, lastUpdateFile)
	if err := os.WriteFile(p, []byte(strconv.FormatInt(time.Now().Unix(), 10)), 0644); err != nil {
		log.Printf("Warning: could not write %s: %v", lastUpdateFile, err)
	}
}

func readAge() time.Duration {
	data, err := os.ReadFile(filepath.Join(dbDir, lastUpdateFile))
	if err != nil {
		return 1<<63 - 1
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 1<<63 - 1
	}
	return time.Since(time.Unix(ts, 0))
}

func readLastModified() string {
	data, err := os.ReadFile(filepath.Join(dbDir, lastModifiedFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func writeLastModified(value string) {
	if value == "" {
		return
	}
	p := filepath.Join(dbDir, lastModifiedFile)
	if err := os.WriteFile(p, []byte(value), 0644); err != nil {
		log.Printf("Warning: could not write %s: %v", lastModifiedFile, err)
	}
}

func swapState(newState *dbState) {
	old := stateValue.Swap(newState)
	if old != nil {
		if s, ok := old.(*dbState); ok {
			s.reader.Close()
		}
	}
}

func installFile(src, dst string) error {
	_ = os.Remove(dst)
	if err := os.Rename(src, dst); err != nil {
		return copyFile(src, dst)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

/* ---------- HTTP client ---------- */

func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

func logProxyConfig(targetURL string) {
	u, err := url.Parse(targetURL)
	if err != nil {
		return
	}
	req := &http.Request{URL: u}
	proxyURL, err := http.ProxyFromEnvironment(req)
	if err != nil || proxyURL == nil {
		log.Printf("Proxy: none (direct connection to %s)", u.Host)
		return
	}
	safe := *proxyURL
	if safe.User != nil {
		if _, hasPwd := safe.User.Password(); hasPwd {
			safe.User = url.UserPassword(safe.User.Username(), "***")
		}
	}
	log.Printf("Proxy: %s (for %s)", safe.String(), u.Host)
}

/* ---------- Lookup ---------- */

// normalizeToISO2 converts ISO2/ISO3/numeric query to an uppercase ISO2 code.
func normalizeToISO2(query string, state *dbState) (string, bool) {
	q := strings.ToUpper(strings.TrimSpace(query))
	if q == "" {
		return "", false
	}
	// Pure numeric (with or without leading zeros).
	if n, err := strconv.Atoi(q); err == nil {
		numCode := fmt.Sprintf("%03d", n)
		if iso2, ok := state.numericToISO2[numCode]; ok {
			return iso2, true
		}
		return "", false
	}
	switch len(q) {
	case 2:
		return q, true
	case 3:
		if iso2, ok := state.iso3ToISO2[q]; ok {
			return iso2, true
		}
		return "", false
	default:
		return "", false
	}
}

func lookupCountry(state *dbState, query string) (*CountryAnswer, error) {
	iso2, ok := normalizeToISO2(query, state)
	if !ok {
		return nil, nil
	}
	network, err := countryToIPv6Net(iso2)
	if err != nil {
		return nil, fmt.Errorf("convert ISO2 to IPv6: %w", err)
	}
	var rec CountryRecord
	if err := state.reader.Lookup(network.IP, &rec); err != nil {
		return nil, err
	}
	if rec.ISO2 == "" {
		return nil, nil
	}
	ans := &CountryAnswer{
		ISO2: rec.ISO2, ISO3: rec.ISO3, NumericCode: rec.NumericCode,
		FIFA: rec.FIFA, Status: rec.Status, Independent: rec.Independent,
		UNMember: rec.UNMember, NameCommon: rec.NameCommon,
		NameOfficial: rec.NameOfficial, Region: rec.Region,
		Subregion: rec.Subregion, Capital: rec.Capital,
		Latitude: rec.Latitude, Longitude: rec.Longitude,
		AreaSqKm: rec.AreaSqKm, Landlocked: rec.Landlocked,
		Borders: orEmpty(rec.Borders), LanguageCodes: orEmpty(rec.LanguageCodes),
		CurrencyCodes: orEmpty(rec.CurrencyCodes), TLDs: orEmpty(rec.TLDs),
		CallingCodes: orEmpty(rec.CallingCodes),
		DemonymEngM: rec.DemonymEngM, DemonymEngF: rec.DemonymEngF,
		GINIYear: rec.GINIYear, GINIValue: rec.GINIValue,
		GDPCurrentUSD: rec.GDPCurrentUSD, GDPPerCapitaUSD: rec.GDPPerCapitaUSD,
		GDPYear: rec.GDPYear, FlagEmoji: rec.FlagEmoji,
		LegalEU: rec.LegalEU, LegalEEA: rec.LegalEEA,
		LegalSchengen: rec.LegalSchengen, LegalGDPR: rec.LegalGDPR,
		LegalDataResidency: rec.LegalDataResidency,
		LegalVATRate: rec.LegalVATRate, LegalVATName: rec.LegalVATName,
		LegalDigitalVAT: rec.LegalDigitalVAT,
		LegalOFAC: rec.LegalOFAC, LegalEUSanction: rec.LegalEUSanction,
		LegalUNSanction: rec.LegalUNSanction, LegalFATF: rec.LegalFATF,
	}
	return ans, nil
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

/* ---------- HTTP Handlers ---------- */

func indexHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func faviconHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Write(faviconPNG)
}

func openapiHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(openapiJSON)
}

func countryHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
			return
		}
		var req CountryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondCountry(w, "ERROR", "", nil)
			return
		}
		defer r.Body.Close()

		q := strings.TrimSpace(req.Country)
		if q == "" {
			respondCountry(w, "ERROR", q, nil)
			return
		}

		stateVal := stateValue.Load()
		if stateVal == nil {
			respondCountry(w, "ERROR", q, nil)
			return
		}
		state := stateVal.(*dbState)

		ans, err := lookupCountry(state, q)
		if err != nil {
			log.Printf("DB lookup error: %v", err)
			respondCountry(w, "ERROR", q, nil)
			return
		}
		if ans == nil {
			respondCountry(w, "NOTFOUND", q, nil)
			return
		}
		respondCountry(w, "SUCCESS", q, ans)
	}
}

func respondCountry(w http.ResponseWriter, status, query string, answer *CountryAnswer) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(CountryResponse{Status: status, Query: query, Answer: answer})
}

func getDBHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mmdbPath := filepath.Join(dbDir, dbFileName)
		if _, err := os.Stat(mmdbPath); err != nil {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		http.ServeFile(w, r, mmdbPath)
	}
}

/* ---------- Peer download ---------- */

func buildLookupMaps(db *maxminddb.Reader) (map[string]string, map[string]string, error) {
	iso3Map := make(map[string]string)
	numMap := make(map[string]string)
	var rec CountryRecord
	networks := db.Networks()
	for networks.Next() {
		if _, err := networks.Network(&rec); err != nil || rec.ISO2 == "" {
			continue
		}
		iso2 := strings.ToUpper(rec.ISO2)
		if rec.ISO3 != "" {
			iso3Map[strings.ToUpper(rec.ISO3)] = iso2
		}
		if rec.NumericCode != "" {
			numMap[fmt.Sprintf("%03s", rec.NumericCode)] = iso2
		}
	}
	return iso3Map, numMap, networks.Err()
}

func downloadFromPeer(ctx context.Context) error {
	u, err := url.Parse(dbURL)
	if err != nil {
		return fmt.Errorf("invalid COUNTRY_DB_URL %q: %w", dbURL, err)
	}
	u.Path = "/db/country"
	peerURL := u.String()
	log.Printf("Downloading country.mmdb from peer: %s", peerURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, peerURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	client := newHTTPClient(120 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("peer GET %s: %w", peerURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("peer returned %s", resp.Status)
	}
	tmpFile, err := os.CreateTemp(dbDir, "country-peer-*.mmdb")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write peer mmdb: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	newDB, err := maxminddb.Open(tmpName)
	if err != nil {
		return fmt.Errorf("open peer mmdb: %w", err)
	}
	iso3Map, numMap, err := buildLookupMaps(newDB)
	if err != nil {
		newDB.Close()
		return fmt.Errorf("build lookup maps: %w", err)
	}
	swapState(&dbState{reader: newDB, iso3ToISO2: iso3Map, numericToISO2: numMap})
	finalPath := filepath.Join(dbDir, dbFileName)
	if err := installFile(tmpName, finalPath); err != nil {
		log.Printf("Warning: could not persist peer mmdb: %v", err)
	}
	writeTimestamp()
	log.Println("Peer mmdb download complete")
	return nil
}

/* ---------- CDN error types ---------- */

var errNotModified = errors.New("CSV not modified (304)")

type errRateLimited struct{ RetryAfter int64 }

func (e *errRateLimited) Error() string {
	return fmt.Sprintf("CDN rate-limited (429) — retry after unix timestamp %d (%s)",
		e.RetryAfter, time.Unix(e.RetryAfter, 0).UTC().Format(time.RFC3339))
}

type errProductGone struct{ Body string }

func (e *errProductGone) Error() string {
	return fmt.Sprintf("CDN product gone (410): %s", e.Body)
}

type errUnauthorized struct{ Message string }

func (e *errUnauthorized) Error() string {
	return fmt.Sprintf("CDN unauthorized (401): %s", e.Message)
}

func extractJSONMessage(body []byte) string {
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &obj); err == nil && obj.Message != "" {
		return obj.Message
	}
	return strings.TrimSpace(string(body))
}

/* ---------- CDN fetch ---------- */

func fetchCSVFromCDN(ctx context.Context) (io.ReadCloser, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cdnCSVURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create CDN request: %w", err)
	}
	req.Header.Set("User-Agent", "http2country/1.0 (+https://github.com/letstool/http2country)")
	if licenseKey != "" {
		req.Header.Set("Authorization", "Basic "+licenseKey)
	}
	if lm := readLastModified(); lm != "" {
		req.Header.Set("If-Modified-Since", lm)
		log.Printf("CDN request with If-Modified-Since: %s", lm)
	}
	client := newHTTPClient(180 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("CDN GET: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusNotModified:
		resp.Body.Close()
		log.Println("CDN: CSV not modified (304) — current DB is up to date")
		return nil, "", errNotModified
	case http.StatusTooManyRequests:
		ra := resp.Header.Get("Retry-After")
		resp.Body.Close()
		ts, _ := strconv.ParseInt(strings.TrimSpace(ra), 10, 64)
		return nil, "", &errRateLimited{RetryAfter: ts}
	case http.StatusGone:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, "", &errProductGone{Body: strings.TrimSpace(string(body))}
	case http.StatusUnauthorized:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, "", &errUnauthorized{Message: extractJSONMessage(body)}
	case http.StatusOK:
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		resp.Body.Close()
		return nil, "", fmt.Errorf("CDN returned %s: %s", resp.Status, body)
	}
	lastModified := resp.Header.Get("Last-Modified")
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		resp.Body.Close()
		return nil, "", fmt.Errorf("CDN gzip reader: %w", err)
	}
	return &gzipReadCloser{gz: gz, body: resp.Body}, lastModified, nil
}

type gzipReadCloser struct {
	gz   *gzip.Reader
	body io.ReadCloser
}

func (g *gzipReadCloser) Read(p []byte) (int, error) { return g.gz.Read(p) }
func (g *gzipReadCloser) Close() error {
	err1 := g.gz.Close()
	err2 := g.body.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

/* ---------- CSV helpers ---------- */

func parseBool(s string) bool { return strings.EqualFold(s, "true") }

func splitSemicolon(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

/* ---------- CSV column indices ---------- */
const (
	colISO2 = 0; colISO3 = 1; colNumeric = 2; colFIFA = 3
	colStatus = 4; colIndependent = 5; colUNMember = 6
	colNameCommon = 7; colNameOfficial = 8; colRegion = 9; colSubregion = 10
	colCapital = 11; colLatitude = 12; colLongitude = 13; colAreaSqKm = 14
	colLandlocked = 15; colBorders = 16; colLanguageCodes = 17
	colCurrencyCodes = 18; colTLD = 19; colCallingCodes = 20
	colDemonymM = 21; colDemonymF = 22; colGINIYear = 23; colGINIValue = 24
	colGDPCurrent = 25; colGDPPerCapita = 26; colGDPYear = 27; colFlagEmoji = 28
	colLegalEU = 29; colLegalEEA = 30; colLegalSchengen = 31; colLegalGDPR = 32
	colLegalDataRes = 33; colLegalVATRate = 34; colLegalVATName = 35
	colLegalDigVAT = 36; colLegalOFAC = 37; colLegalEUSanct = 38
	colLegalUNSanct = 39; colLegalFATF = 40
	minColumns = 40 // at minimum we need up to col 39
)

/* ---------- Build DB from CSV ---------- */

func buildCountryDBFromCSV(ctx context.Context) error {
	log.Printf("Fetching Country CSV from CDN: %s", cdnCSVURL)
	csvReader, lastModified, err := fetchCSVFromCDN(ctx)
	if err != nil {
		if err == errNotModified {
			writeTimestamp()
			return nil
		}
		return fmt.Errorf("CDN fetch: %w", err)
	}
	defer csvReader.Close()

	writer, err := mmdbwriter.New(mmdbwriter.Options{
		DatabaseType: "http2country-CountryDB",
		Description:  map[string]string{"en": "Country Information Database built by http2country"},
		RecordSize:   28,
		IPVersion:    6,
		IncludeReservedNetworks: true,
	})
	if err != nil {
		return fmt.Errorf("create mmdb writer: %w", err)
	}

	iso3Map := make(map[string]string)
	numMap := make(map[string]string)

	r := csv.NewReader(csvReader)
	r.ReuseRecord = false
	r.FieldsPerRecord = -1

	if _, err := r.Read(); err != nil {
		return fmt.Errorf("read CSV header: %w", err)
	}

	inserted, lineNum := 0, 1
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("Warning: CSV parse error at line %d: %v — skipping", lineNum+1, err)
			lineNum++
			continue
		}
		lineNum++
		if len(record) < minColumns {
			log.Printf("Warning: CSV line %d has %d columns (need %d) — skipping", lineNum, len(record), minColumns)
			continue
		}

		iso2 := strings.ToUpper(strings.TrimSpace(record[colISO2]))
		if len(iso2) != 2 {
			log.Printf("Warning: invalid ISO2 %q at line %d — skipping", iso2, lineNum)
			continue
		}

		iso3 := strings.ToUpper(strings.TrimSpace(record[colISO3]))
		numCode := strings.TrimSpace(record[colNumeric])
		if iso3 != "" {
			iso3Map[iso3] = iso2
		}
		if numCode != "" {
			numMap[fmt.Sprintf("%03s", numCode)] = iso2
		}

		lat, _ := strconv.ParseFloat(record[colLatitude], 64)
		lon, _ := strconv.ParseFloat(record[colLongitude], 64)
		area, _ := strconv.ParseFloat(record[colAreaSqKm], 64)
		giniVal, _ := strconv.ParseFloat(record[colGINIValue], 64)
		gdpCurr, _ := strconv.ParseFloat(record[colGDPCurrent], 64)
		gdpPCap, _ := strconv.ParseFloat(record[colGDPPerCapita], 64)
		vatRate, _ := strconv.ParseFloat(record[colLegalVATRate], 64)

		toSlice := func(s string) mmdbtype.Slice {
			parts := splitSemicolon(s)
			sl := make(mmdbtype.Slice, len(parts))
			for i, p := range parts {
				sl[i] = mmdbtype.String(p)
			}
			return sl
		}
		fatf := ""
		if len(record) > colLegalFATF {
			fatf = strings.TrimSpace(record[colLegalFATF])
		}

		mmdbRec := mmdbtype.Map{
			"iso2": mmdbtype.String(iso2), "iso3": mmdbtype.String(iso3),
			"numeric_code": mmdbtype.String(numCode), "fifa": mmdbtype.String(record[colFIFA]),
			"status": mmdbtype.String(record[colStatus]),
			"independent": mmdbtype.Bool(parseBool(record[colIndependent])),
			"un_member": mmdbtype.Bool(parseBool(record[colUNMember])),
			"name_common": mmdbtype.String(record[colNameCommon]),
			"name_official": mmdbtype.String(record[colNameOfficial]),
			"region": mmdbtype.String(record[colRegion]),
			"subregion": mmdbtype.String(record[colSubregion]),
			"capital": mmdbtype.String(record[colCapital]),
			"latitude": mmdbtype.Float64(lat), "longitude": mmdbtype.Float64(lon),
			"area_sq_km": mmdbtype.Float64(area),
			"landlocked": mmdbtype.Bool(parseBool(record[colLandlocked])),
			"borders": toSlice(record[colBorders]),
			"language_codes": toSlice(record[colLanguageCodes]),
			"currency_codes": toSlice(record[colCurrencyCodes]),
			"tld": toSlice(record[colTLD]),
			"calling_codes": toSlice(record[colCallingCodes]),
			"demonym_eng_m": mmdbtype.String(record[colDemonymM]),
			"demonym_eng_f": mmdbtype.String(record[colDemonymF]),
			"gini_year": mmdbtype.String(record[colGINIYear]),
			"gini_value": mmdbtype.Float64(giniVal),
			"gdp_current_usd": mmdbtype.Float64(gdpCurr),
			"gdp_per_capita_usd": mmdbtype.Float64(gdpPCap),
			"gdp_year": mmdbtype.String(record[colGDPYear]),
			"flag_emoji": mmdbtype.String(record[colFlagEmoji]),
			"legal_eu": mmdbtype.Bool(parseBool(record[colLegalEU])),
			"legal_eea": mmdbtype.Bool(parseBool(record[colLegalEEA])),
			"legal_schengen": mmdbtype.Bool(parseBool(record[colLegalSchengen])),
			"legal_gdpr": mmdbtype.Bool(parseBool(record[colLegalGDPR])),
			"legal_data_residency": mmdbtype.Bool(parseBool(record[colLegalDataRes])),
			"legal_vat_rate": mmdbtype.Float64(vatRate),
			"legal_vat_name": mmdbtype.String(record[colLegalVATName]),
			"legal_digital_vat": mmdbtype.Bool(parseBool(record[colLegalDigVAT])),
			"legal_ofac": mmdbtype.Bool(parseBool(record[colLegalOFAC])),
			"legal_eu_sanction": mmdbtype.Bool(parseBool(record[colLegalEUSanct])),
			"legal_un_sanction": mmdbtype.Bool(parseBool(record[colLegalUNSanct])),
			"legal_fatf": mmdbtype.String(fatf),
		}

		network, err := countryToIPv6Net(iso2)
		if err != nil {
			log.Printf("Warning: could not build network for %s: %v", iso2, err)
			continue
		}
		if err := writer.Insert(network, mmdbRec); err != nil {
			log.Printf("Warning: failed to insert %s: %v", iso2, err)
			continue
		}
		inserted++
	}
	log.Printf("Parsed %d country records from CDN CSV", inserted)

	tmpFile, err := os.CreateTemp(dbDir, "country-build-*.mmdb")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	if _, err := writer.WriteTo(tmpFile); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write mmdb: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	newDB, err := maxminddb.Open(tmpName)
	if err != nil {
		return fmt.Errorf("open new mmdb: %w", err)
	}
	swapState(&dbState{reader: newDB, iso3ToISO2: iso3Map, numericToISO2: numMap})
	finalPath := filepath.Join(dbDir, dbFileName)
	if err := installFile(tmpName, finalPath); err != nil {
		return fmt.Errorf("install mmdb: %w", err)
	}
	writeTimestamp()
	writeLastModified(lastModified)
	log.Printf("Country DB built from CDN CSV: %d countries inserted", inserted)
	return nil
}

/* ---------- DB dispatch ---------- */

func updateDB(ctx context.Context) error {
	if dbURL != "" {
		return downloadFromPeer(ctx)
	}
	return buildCountryDBFromCSV(ctx)
}

func ensureDB(ctx context.Context) error {
	mmdbPath := filepath.Join(dbDir, dbFileName)
	if _, err := os.Stat(mmdbPath); err == nil {
		age := readAge()
		if age < updateInterval {
			db, err := maxminddb.Open(mmdbPath)
			if err != nil {
				return fmt.Errorf("open existing database: %w", err)
			}
			iso3Map, numMap, err := buildLookupMaps(db)
			if err != nil {
				db.Close()
				return fmt.Errorf("build lookup maps: %w", err)
			}
			stateValue.Store(&dbState{reader: db, iso3ToISO2: iso3Map, numericToISO2: numMap})
			log.Printf("Loaded existing Country DB (built %s ago, max age %s)",
				age.Round(time.Minute), updateInterval)
			return nil
		}
		log.Printf("Country DB is %s old (max %s), updating...", age.Round(time.Minute), updateInterval)
	}
	return updateDB(ctx)
}

/* ---------- Scheduler ---------- */

var goneRetrySchedule = []time.Duration{
	24 * time.Hour, 48 * time.Hour, 72 * time.Hour, 96 * time.Hour,
}

func schedulePeriodicUpdate(ctx context.Context) {
	mode := "CDN CSV build"
	if dbURL != "" {
		mode = "peer download (" + dbURL + ")"
	}
	log.Printf("Country DB auto-refresh every %s [mode: %s]", updateInterval, mode)
	go func() {
		goneAttempt := 0
		timer := time.NewTimer(updateInterval)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				err := updateDB(ctx)
				if err == nil {
					if goneAttempt > 0 {
						log.Printf("CDN: update succeeded after %d gone-retry attempt(s) — counter reset", goneAttempt)
						goneAttempt = 0
					}
					timer.Reset(updateInterval)
					continue
				}
				var rl *errRateLimited
				if errors.As(err, &rl) && rl.RetryAfter > 0 {
					wait := time.Until(time.Unix(rl.RetryAfter, 0))
					if wait <= 0 {
						wait = updateInterval
					}
					log.Printf("Rate-limited by CDN: next attempt in %s", wait.Round(time.Second))
					timer.Reset(wait)
					continue
				}
				var gone *errProductGone
				if errors.As(err, &gone) {
					if goneAttempt >= len(goneRetrySchedule) {
						log.Printf("CDN [410] All retry attempts exhausted — stopping permanently.")
						return
					}
					wait := goneRetrySchedule[goneAttempt]
					log.Printf("CDN [410] Product gone (attempt %d/%d) — retry in %s.", goneAttempt+1, len(goneRetrySchedule), wait)
					goneAttempt++
					timer.Reset(wait)
					continue
				}
				var unauth *errUnauthorized
				if errors.As(err, &unauth) {
					log.Printf("CDN [401] Authorization refused — stopping permanently. Message: %s", unauth.Message)
					return
				}
				log.Printf("Scheduled update failed: %v — retrying in %s", err, updateInterval)
				timer.Reset(updateInterval)
			case <-ctx.Done():
				return
			}
		}
	}()
}

/* ---------- Main ---------- */

func main() {
	const sentinel = "\x00"
	flagDBURL      := flag.String("db-url",      sentinel, "Base URL of a peer http2country instance. Overrides COUNTRY_DB_URL.")
	flagDBDir      := flag.String("db-dir",      sentinel, "Directory for the mmdb file. Overrides COUNTRY_DB_DIR. Default: /data")
	flagListenAddr := flag.String("listen-addr", sentinel, "Listen address. Overrides LISTEN_ADDR. Default: 127.0.0.1:8080")
	flagLicenseKey := flag.String("license-key", sentinel, "CDN license key. Overrides LICENSE_KEY. Optional.")
	flag.Parse()

	resolve := func(flagVal, envKey, defaultVal string) string {
		if flagVal != sentinel {
			return flagVal
		}
		if v := os.Getenv(envKey); v != "" {
			return v
		}
		return defaultVal
	}

	dbURL      = resolve(*flagDBURL,      "COUNTRY_DB_URL", "")
	dbDir      = resolve(*flagDBDir,      "COUNTRY_DB_DIR", "/data")
	listenAddr = resolve(*flagListenAddr, "LISTEN_ADDR",    "127.0.0.1:8080")
	licenseKey = resolve(*flagLicenseKey, "LICENSE_KEY",    "")

	switch {
	case dbURL != "":
		log.Printf("Mode: peer sync from %s (interval: %s)", dbURL, updateInterval)
		logProxyConfig(dbURL)
	case licenseKey != "":
		log.Printf("Mode: CDN CSV build — licensed (interval: %s)", updateInterval)
		logProxyConfig(cdnCSVURL)
	default:
		log.Printf("Mode: CDN CSV build — anonymous (interval: %s)", updateInterval)
		logProxyConfig(cdnCSVURL)
	}

	if err := os.MkdirAll(dbDir, 0755); err != nil {
		log.Fatalf("failed to create directory %s: %v", dbDir, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ensureDB(ctx); err != nil {
		log.Fatalf("failed to initialize Country database: %v", err)
	}

	schedulePeriodicUpdate(ctx)

	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/favicon.png", faviconHandler)
	http.HandleFunc("/openapi.json", openapiHandler)
	http.HandleFunc("/api/v1/country", countryHandler())
	http.HandleFunc("/db/country", getDBHandler())

	srv := &http.Server{
		Addr:         listenAddr,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	log.Printf("http2country server listening on %s", listenAddr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}
