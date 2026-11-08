package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web/*
var files embed.FS

var (
	allowedEvents = map[string]bool{"page_view": true, "start_match": true, "complete_match": true, "share_match": true, "save_match": true, "open_saved": true}
	validPath     = regexp.MustCompile(`^/[a-zA-Z0-9/_-]{0,80}$`)
	validCountry  = regexp.MustCompile(`^[A-Z]{2}$`)
)

type app struct {
	store         *eventStore
	adminPassword string
	secret        []byte
	httpClient    *http.Client
	geoMu         sync.Mutex
	geoCache      map[string]geoResult
	allowProxyGeo bool
	geoEnabled    bool
	loginMu       sync.Mutex
	loginAttempts map[string][]time.Time
}

type geoResult struct {
	country string
	expires time.Time
}

type event struct {
	ID           string    `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	EventType    string    `json:"event_type"`
	SessionHash  string    `json:"session_hash"`
	Country      string    `json:"country"`
	Source       string    `json:"source"`
	Campaign     string    `json:"campaign"`
	ReferrerHost string    `json:"referrer_host"`
	Pathname     string    `json:"pathname"`
}

type eventInput struct {
	Type     string `json:"type"`
	Session  string `json:"session"`
	Path     string `json:"path"`
	Source   string `json:"source"`
	Campaign string `json:"campaign"`
	Referrer string `json:"referrer"`
}

func main() {
	loadEnvFile(".env")
	key := []byte(os.Getenv("ANALYTICS_SECRET"))
	if len(key) < 16 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			log.Fatal(err)
		}
		log.Print("ANALYTICS_SECRET is not set: using temporary signing key (admin cookies reset on restart)")
	}
	store, err := newEventStore(os.Getenv("SUPABASE_URL"), firstNonempty(os.Getenv("SUPABASE_SECRET_KEY"), os.Getenv("SUPABASE_SERVICE_ROLE_KEY")), firstNonempty(os.Getenv("DATA_FILE"), "data/analytics.jsonl"))
	if err != nil {
		log.Fatal(err)
	}
	a := &app{store: store, adminPassword: os.Getenv("ADMIN_PASSWORD"), secret: key, httpClient: &http.Client{Timeout: 2500 * time.Millisecond}, geoCache: map[string]geoResult{}, allowProxyGeo: os.Getenv("TRUST_PROXY_GEO") == "1", geoEnabled: os.Getenv("COUNTRY_LOOKUP") != "off", loginAttempts: map[string][]time.Time{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "storage": store.mode(), "time": time.Now().UTC().Format(time.RFC3339)})
	})
	mux.HandleFunc("POST /api/match", a.handleMatch)
	mux.HandleFunc("GET /api/share", a.handleShare)
	mux.HandleFunc("POST /api/events", a.handleEvent)
	mux.HandleFunc("POST /api/admin/login", a.handleLogin)
	mux.HandleFunc("POST /api/admin/logout", a.handleLogout)
	mux.HandleFunc("GET /api/admin/stats", a.handleStats)
	mux.HandleFunc("GET /api/admin/export", a.handleCSV)
	static, err := fs.Sub(files, "web")
	if err != nil {
		log.Fatal(err)
	}
	staticServer := http.FileServer(http.FS(static))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" || path == "match" || path == "result" || path == "saved" || path == "privacy" || path == "about" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			serveAppHTML(w, r, static)
			return
		}
		if path == "admin" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			http.ServeFileFS(w, r, static, "admin.html")
			return
		}
		if _, err := fs.Stat(static, path); err != nil {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(path, ".webmanifest") {
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		if strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".css") {
			w.Header().Set("Cache-Control", "public, max-age=300")
		}
		staticServer.ServeHTTP(w, r)
	})
	port := firstNonempty(os.Getenv("PORT"), "8080")
	server := &http.Server{Addr: ":" + port, Handler: secHeaders(mux), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 25 * time.Second, IdleTimeout: 60 * time.Second}
	log.Printf("Astro Matcher ready at http://localhost:%s (storage: %s)", port, store.mode())
	if a.adminPassword == "" {
		log.Print("ADMIN_PASSWORD not set: analytics dashboard login is disabled")
	}
	log.Fatal(server.ListenAndServe())
}

func secHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		h.ServeHTTP(w, r)
	})
}

func loadEnvFile(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), "\"'")
		if _, exists := os.LookupEnv(k); !exists {
			_ = os.Setenv(k, v)
		}
	}
}
func firstNonempty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
func readJSON(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("extra data")
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func errorJSON(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (a *app) handleMatch(w http.ResponseWriter, r *http.Request) {
	var req matchRequest
	if err := readJSON(w, r, &req); err != nil {
		errorJSON(w, 400, "Invalid JSON input")
		return
	}
	out, err := buildMatch(req)
	if err != nil {
		errorJSON(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, out)
}
func (a *app) handleShare(w http.ResponseWriter, r *http.Request) {
	s1, s2 := r.URL.Query().Get("a"), r.URL.Query().Get("b")
	if !isSign(s1) || !isSign(s2) {
		errorJSON(w, 400, "Unknown zodiac signs")
		return
	}
	writeJSON(w, 200, buildFromSigns(s1, s2, "You", "Them"))
}
func (a *app) handleEvent(w http.ResponseWriter, r *http.Request) {
	var input eventInput
	if err := readJSON(w, r, &input); err != nil {
		errorJSON(w, 400, "Invalid event")
		return
	}
	if !allowedEvents[input.Type] || len(input.Session) < 12 || len(input.Session) > 110 || !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(input.Session) {
		errorJSON(w, 400, "Invalid event type or session")
		return
	}
	path := input.Path
	if !validPath.MatchString(path) {
		path = "/"
	}
	refHost := ""
	if ref, err := url.Parse(input.Referrer); err == nil && (ref.Scheme == "https" || ref.Scheme == "http") {
		refHost = cleanString(ref.Hostname(), 100)
	}
	source := sourceName(cleanString(input.Source, 80), refHost)
	sessionHash := a.hash(input.Session)
	country := a.country(r)
	id := randomHex(16)
	e := event{ID: id, CreatedAt: time.Now().UTC(), EventType: input.Type, SessionHash: sessionHash, Country: country, Source: source, Campaign: cleanString(input.Campaign, 100), ReferrerHost: refHost, Pathname: path}
	if err := a.store.appendEvent(r.Context(), e); err != nil {
		log.Printf("analytics write failed: %v", err)
		errorJSON(w, 503, "Analytics storage unavailable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func cleanString(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = s[:n]
	}
	return strings.Map(func(r rune) rune {
		if r < ' ' || r == 127 {
			return -1
		}
		return r
	}, s)
}
func sourceName(utm, ref string) string {
	hay := strings.ToLower(utm + " " + ref)
	for _, p := range []struct{ sub, out string }{{"tiktok", "TikTok"}, {"instagram", "Instagram"}, {"youtube", "YouTube"}, {"youtu.be", "YouTube"}, {"google", "Google"}, {"bing", "Bing"}, {"reddit", "Reddit"}, {"pinterest", "Pinterest"}, {"facebook", "Facebook"}, {"threads", "Threads"}, {"telegram", "Telegram"}, {"chatgpt", "ChatGPT"}, {"twitter", "X / Twitter"}, {"x.com", "X / Twitter"}} {
		if strings.Contains(hay, p.sub) {
			return p.out
		}
	}
	if utm != "" {
		return cleanString(utm, 48)
	}
	if ref != "" {
		return "Referral"
	}
	return "Direct / unknown"
}
func (a *app) hash(s string) string {
	h := hmac.New(sha256.New, a.secret)
	_, _ = h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))[:32]
}
func randomHex(bytes int) string {
	b := make([]byte, bytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
func clientIP(r *http.Request, trust bool) string {
	raw := r.RemoteAddr
	if trust {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			raw = strings.TrimSpace(strings.Split(forwarded, ",")[0])
		}
	}
	if ip, _, err := net.SplitHostPort(raw); err == nil {
		raw = ip
	}
	if parsed := net.ParseIP(strings.TrimSpace(raw)); parsed != nil {
		return parsed.String()
	}
	return ""
}
func (a *app) country(r *http.Request) string {
	if a.allowProxyGeo {
		if country := strings.ToUpper(r.Header.Get("CF-IPCountry")); validCountry.MatchString(country) && country != "XX" && country != "T1" {
			return country
		}
	}
	if !a.geoEnabled {
		return "Unknown"
	}
	ip := clientIP(r, a.allowProxyGeo)
	netIP := net.ParseIP(ip)
	if netIP == nil || netIP.IsPrivate() || netIP.IsLoopback() || netIP.IsLinkLocalUnicast() {
		return "Unknown"
	}
	key := a.hash(ip)
	a.geoMu.Lock()
	cached, ok := a.geoCache[key]
	a.geoMu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.country
	}
	// Free commercial-use IP-country lookup; IP is sent only to the lookup provider, never persisted by Astro Matcher.
	ctx := r.Context()
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.country.is/"+url.PathEscape(ip), nil)
	if err != nil {
		return "Unknown"
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "Unknown"
	}
	defer resp.Body.Close()
	var result struct {
		Country string `json:"country"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 2000)).Decode(&result) != nil {
		return "Unknown"
	}
	val := strings.ToUpper(result.Country)
	if !validCountry.MatchString(val) {
		val = "Unknown"
	}
	a.geoMu.Lock()
	if len(a.geoCache) > 5000 {
		a.geoCache = map[string]geoResult{}
	}
	a.geoCache[key] = geoResult{country: val, expires: time.Now().Add(24 * time.Hour)}
	a.geoMu.Unlock()
	return val
}

func (a *app) loginAllowed(ip string) bool {
	now := time.Now()
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	attempts := a.loginAttempts[ip][:0]
	for _, t := range a.loginAttempts[ip] {
		if now.Sub(t) < 10*time.Minute {
			attempts = append(attempts, t)
		}
	}
	if len(attempts) >= 8 {
		a.loginAttempts[ip] = attempts
		return false
	}
	a.loginAttempts[ip] = append(attempts, now)
	if len(a.loginAttempts) > 2000 {
		a.loginAttempts = map[string][]time.Time{}
	}
	return true
}
func (a *app) handleLogin(w http.ResponseWriter, r *http.Request) {
	if a.adminPassword == "" {
		errorJSON(w, 503, "Set ADMIN_PASSWORD in server environment first")
		return
	}
	if !a.loginAllowed(clientIP(r, true)) {
		errorJSON(w, 429, "Too many login attempts. Try later.")
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &input); err != nil {
		errorJSON(w, 400, "Invalid request")
		return
	}
	candidate := sha256.Sum256([]byte(input.Password))
	target := sha256.Sum256([]byte(a.adminPassword))
	if subtle.ConstantTimeCompare(candidate[:], target[:]) != 1 {
		errorJSON(w, 401, "Incorrect password")
		return
	}
	expires := time.Now().Add(24 * time.Hour).Unix()
	payload := fmt.Sprintf("v1.%d", expires)
	h := hmac.New(sha256.New, a.secret)
	_, _ = h.Write([]byte(payload))
	signed := payload + "." + hex.EncodeToString(h.Sum(nil))
	http.SetCookie(w, &http.Cookie{Name: "astro_admin", Value: signed, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", MaxAge: 86400})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *app) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	c, err := r.Cookie("astro_admin")
	if err != nil {
		errorJSON(w, 401, "Login required")
		return false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		errorJSON(w, 401, "Invalid session")
		return false
	}
	expire, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > expire {
		errorJSON(w, 401, "Session expired")
		return false
	}
	payload := parts[0] + "." + parts[1]
	h := hmac.New(sha256.New, a.secret)
	_, _ = h.Write([]byte(payload))
	expected := hex.EncodeToString(h.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		errorJSON(w, 401, "Invalid session")
		return false
	}
	return true
}
func (a *app) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "astro_admin", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func daysParam(r *http.Request) int {
	d, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if d == 7 || d == 90 {
		return d
	}
	return 30
}
func (a *app) handleStats(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	days := daysParam(r)
	evts, truncated, err := a.store.readEvents(r.Context(), days, 30000)
	if err != nil {
		errorJSON(w, 503, "Could not read analytics: "+err.Error())
		return
	}
	writeJSON(w, 200, makeStats(evts, days, truncated, a.store.mode()))
}
func (a *app) handleCSV(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	days := daysParam(r)
	evts, _, err := a.store.readEvents(r.Context(), days, 30000)
	if err != nil {
		errorJSON(w, 503, "Analytics unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=astromatcher-events-%dd.csv", days))
	w.Header().Set("Cache-Control", "no-store")
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"created_at_utc", "event_type", "session_hash", "country", "source", "campaign", "referrer_host", "pathname"})
	for _, e := range evts {
		_ = writer.Write([]string{e.CreatedAt.Format(time.RFC3339), e.EventType, e.SessionHash, e.Country, e.Source, e.Campaign, e.ReferrerHost, e.Pathname})
	}
	writer.Flush()
}

type countItem struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}
type dayStat struct {
	Date     string `json:"date"`
	Views    int    `json:"views"`
	Sessions int    `json:"sessions"`
	Matches  int    `json:"matches"`
}
type stats struct {
	Days        int         `json:"days"`
	Storage     string      `json:"storage"`
	Truncated   bool        `json:"truncated"`
	TotalEvents int         `json:"totalEvents"`
	Views       int         `json:"views"`
	Sessions    int         `json:"sessions"`
	Matches     int         `json:"matches"`
	Shares      int         `json:"shares"`
	Saves       int         `json:"saves"`
	Conversion  float64     `json:"conversion"`
	Countries   []countItem `json:"countries"`
	Sources     []countItem `json:"sources"`
	EventTypes  []countItem `json:"eventTypes"`
	Daily       []dayStat   `json:"daily"`
	Recent      []event     `json:"recent"`
}

func makeStats(evts []event, days int, truncated bool, mode string) stats {
	s := stats{Days: days, Storage: mode, Truncated: truncated, TotalEvents: len(evts), Countries: []countItem{}, Sources: []countItem{}, EventTypes: []countItem{}, Recent: []event{}}
	seen := map[string]bool{}
	countries := map[string]int{}
	sources := map[string]int{}
	types := map[string]int{}
	daily := map[string]*dayStat{}
	dayVisitors := map[string]map[string]bool{}
	for d := days - 1; d >= 0; d-- {
		key := time.Now().UTC().AddDate(0, 0, -d).Format("2006-01-02")
		daily[key] = &dayStat{Date: key}
		dayVisitors[key] = map[string]bool{}
	}
	for i, e := range evts {
		if i < 20 {
			s.Recent = append(s.Recent, e)
		}
		types[e.EventType]++
		if e.EventType == "page_view" {
			s.Views++
			countries[e.Country]++
			sources[e.Source]++
			day := e.CreatedAt.UTC().Format("2006-01-02")
			if d := daily[day]; d != nil {
				d.Views++
			}
		}
		if e.EventType == "complete_match" {
			s.Matches++
			if d := daily[e.CreatedAt.UTC().Format("2006-01-02")]; d != nil {
				d.Matches++
			}
		}
		if e.EventType == "share_match" {
			s.Shares++
		}
		if e.EventType == "save_match" {
			s.Saves++
		}
		if !seen[e.SessionHash] {
			seen[e.SessionHash] = true
		}
		day := e.CreatedAt.UTC().Format("2006-01-02")
		if dayVisitors[day] != nil {
			dayVisitors[day][e.SessionHash] = true
		}
	}
	s.Sessions = len(seen)
	if s.Sessions > 0 {
		s.Conversion = float64(s.Matches) / float64(s.Sessions) * 100
	}
	for _, d := range daily {
		d.Sessions = len(dayVisitors[d.Date])
		s.Daily = append(s.Daily, *d)
	}
	sort.Slice(s.Daily, func(i, j int) bool { return s.Daily[i].Date < s.Daily[j].Date })
	s.Countries = topCounts(countries, 20)
	s.Sources = topCounts(sources, 20)
	s.EventTypes = topCounts(types, 20)
	return s
}
func topCounts(counts map[string]int, max int) []countItem {
	out := make([]countItem, 0, len(counts))
	for k, v := range counts {
		out = append(out, countItem{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Name < out[j].Name
		}
		return out[i].Count > out[j].Count
	})
	if len(out) > max {
		out = out[:max]
	}
	return out
}

// eventStore persists locally for self-hosting, or uses Supabase's PostgREST with a server-side secret key.
type eventStore struct {
	mu             sync.Mutex
	filename       string
	supabaseURL    string
	supabaseSecret string
	client         *http.Client
}

func newEventStore(base, secret, filename string) (*eventStore, error) {
	if (base == "") != (secret == "") {
		return nil, errors.New("set BOTH SUPABASE_URL and SUPABASE_SECRET_KEY, or neither")
	}
	s := &eventStore{filename: filename, supabaseURL: strings.TrimSuffix(base, "/"), supabaseSecret: secret, client: &http.Client{Timeout: 8 * time.Second}}
	if base == "" {
		if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		_ = f.Close()
	}
	return s, nil
}
func (s *eventStore) mode() string {
	if s.supabaseURL != "" {
		return "supabase"
	}
	return "local-jsonl"
}
func (s *eventStore) supabaseReq(ctx context.Context, method, urlstr string, body io.Reader) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, urlstr, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("apikey", s.supabaseSecret)
	// Legacy service_role is a JWT; new sb_secret_ keys must not be sent as bearer tokens.
	if !strings.HasPrefix(s.supabaseSecret, "sb_secret_") {
		request.Header.Set("Authorization", "Bearer "+s.supabaseSecret)
	}
	request.Header.Set("Content-Type", "application/json")
	return s.client.Do(request)
}
func (s *eventStore) appendEvent(ctx context.Context, e event) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if s.supabaseURL != "" {
		resp, err := s.supabaseReq(ctx, "POST", s.supabaseURL+"/rest/v1/analytics_events", strings.NewReader(string(raw)))
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 201 && resp.StatusCode != 204 {
			return fmt.Errorf("supabase returned %d: %s", resp.StatusCode, limitedBody(resp.Body))
		}
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(raw, '\n'))
	return err
}
func limitedBody(r io.Reader) string { b, _ := io.ReadAll(io.LimitReader(r, 500)); return string(b) }
func (s *eventStore) readEvents(ctx context.Context, days, max int) ([]event, bool, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	if s.supabaseURL != "" {
		result := make([]event, 0, 1024)
		for offset := 0; offset < max; offset += 1000 {
			u := s.supabaseURL + "/rest/v1/analytics_events?select=id,created_at,event_type,session_hash,country,source,campaign,referrer_host,pathname&created_at=gte." + url.QueryEscape(cutoff.Format(time.RFC3339)) + "&order=created_at.desc&limit=1000&offset=" + strconv.Itoa(offset)
			resp, err := s.supabaseReq(ctx, "GET", u, nil)
			if err != nil {
				return nil, false, err
			}
			if resp.StatusCode != 200 {
				errorMsg := limitedBody(resp.Body)
				_ = resp.Body.Close()
				return nil, false, fmt.Errorf("supabase returned %d: %s", resp.StatusCode, errorMsg)
			}
			var batch []event
			err = json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(&batch)
			_ = resp.Body.Close()
			if err != nil {
				return nil, false, err
			}
			result = append(result, batch...)
			if len(batch) < 1000 {
				return result, false, nil
			}
		}
		return result, true, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.Open(s.filename)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	// Read cap: 12 MB is enough for initial launch and avoids unbounded memory on a small free host.
	// Read only the newest part of large JSONL logs, so recent analytics stay visible.
	truncatedByFile := false
	if info, statErr := f.Stat(); statErr == nil && info.Size() > 12*1024*1024 {
		_, _ = f.Seek(-12*1024*1024, io.SeekEnd)
		truncatedByFile = true
	}
	bytes, err := io.ReadAll(io.LimitReader(f, 12*1024*1024))
	if err != nil {
		return nil, false, err
	}
	if truncatedByFile {
		if i := strings.IndexByte(string(bytes), '\n'); i >= 0 {
			bytes = bytes[i+1:]
		}
	}
	lines := strings.Split(string(bytes), "\n")
	evts := make([]event, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var e event
		if json.Unmarshal([]byte(line), &e) == nil && !e.CreatedAt.Before(cutoff) {
			evts = append(evts, e)
		}
	}
	sort.Slice(evts, func(i, j int) bool { return evts[i].CreatedAt.After(evts[j].CreatedAt) })
	if len(evts) > max {
		return evts[:max], true, nil
	}
	return evts, truncatedByFile, nil
}

// serveAppHTML provides absolute social preview URLs and sign-pair-specific link titles.
func serveAppHTML(w http.ResponseWriter, r *http.Request, static fs.FS) {
	raw, err := fs.ReadFile(static, "index.html")
	if err != nil {
		http.Error(w, "Site unavailable", 500)
		return
	}
	body := string(raw)
	title := "Astro Matcher | Discover your cosmic chemistry"
	description := "Two people. One cosmic story. Explore your zodiac chemistry with a free, beautiful reading."
	if r.URL.Path == "/result" {
		a, b := r.URL.Query().Get("a"), r.URL.Query().Get("b")
		if isSign(a) && isSign(b) {
			title = signByKey(a).Name + " & " + signByKey(b).Name + " compatibility | Astro Matcher"
			description = "Discover the cosmic story of " + signByKey(a).Name + " and " + signByKey(b).Name + ". Explore this free astrology-inspired reading."
		}
	}
	imageURL := "/og-image.png"
	if r.Host != "" && !strings.ContainsAny(r.Host, " /<>\"'") {
		scheme := "https"
		if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
			scheme = "http"
		}
		imageURL = scheme + "://" + r.Host + "/og-image.png"
	}
	body = strings.Replace(body, `content="Astro Matcher | Discover your cosmic chemistry"`, `content="`+html.EscapeString(title)+`"`, 1)
	body = strings.Replace(body, `content="Two people. One cosmic story. Explore your zodiac chemistry with a free, beautiful reading."`, `content="`+html.EscapeString(description)+`"`, 1)
	body = strings.Replace(body, `content="/og-image.png"`, `content="`+html.EscapeString(imageURL)+`"`, 1)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(200)
	_, _ = io.WriteString(w, body)
}
