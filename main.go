package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Configuration
var (
	defaultPort = "8080"
)

// Kineflex API response structures
type KineflexResponse struct {
	Success bool              `json:"success"`
	Status  bool              `json:"status"`
	Message string            `json:"message,omitempty"`
	Data    KineflexData      `json:"data"`
	Error   *KineflexAPIError `json:"error,omitempty"`
}

type KineflexAPIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Required  int    `json:"required,omitempty"`
	Remaining int    `json:"remaining,omitempty"`
	Field     string `json:"field,omitempty"`
}

type KineflexData struct {
	ID       interface{}       `json:"id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Season   int               `json:"season,omitempty"`
	Episode  int               `json:"episode,omitempty"`
	URL      string            `json:"url"`
	Poster   string            `json:"poster,omitempty"`
	Backdrop string            `json:"backdrop,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
}

// MediaPayload represents video data injected into player.html
type MediaPayload struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Poster  string `json:"poster,omitempty"`
	Type    string `json:"type,omitempty"`
	Season  int    `json:"season,omitempty"`
	Episode int    `json:"episode,omitempty"`
}

// ErrorPayload represents error details injected into player.html or returned as JSON
type ErrorPayload struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	HTTPStatus  int    `json:"httpStatus"`
	Required    int    `json:"required,omitempty"`
	Remaining   int    `json:"remaining,omitempty"`
	Field       string `json:"field,omitempty"`
	RetryAfter  string `json:"retryAfter,omitempty"`
	RequestID   string `json:"requestId,omitempty"`
	KeyProvided bool   `json:"keyProvided"`
}

var (
	movieRegex = regexp.MustCompile(`^/(?:movie|move)/([^/?#]+)`)
	tvRegex    = regexp.MustCompile(`^/tv/([^/?#]+)/([^/?#]+)/([^/?#]+)`)
	apiMovie   = regexp.MustCompile(`^/api/(?:movie|move)/([^/?#]+)`)
	apiTV      = regexp.MustCompile(`^/api/tv/([^/?#]+)/([^/?#]+)/([^/?#]+)`)
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      corsMiddleware(http.HandlerFunc(handleRequest)),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("=====================================================")
	log.Printf("  Kineflex Go Worker & Web Player running on :%s", port)
	log.Printf("  - Movie stream:   http://localhost:%s/movie/550?key=<key>", port)
	log.Printf("  - TV stream:      http://localhost:%s/tv/259909/2/2?key=<key>", port)
	log.Printf("  - Direct stream:  http://localhost:%s/play=<videourl>", port)
	log.Printf("  - Account check:  http://localhost:%s/v1/account?key=<key>", port)
	log.Printf("=====================================================")

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}

// corsMiddleware adds standard CORS headers
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, HEAD")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key, Range")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleRequest dispatches all requests
func handleRequest(w http.ResponseWriter, r *http.Request) {
	reqPath := r.URL.Path
	reqURI := r.RequestURI

	// 1. Direct stream: /play=...
	playURL := extractPlayURL(reqPath, reqURI, r.URL.Query())
	if playURL != "" {
		handleDirectPlay(w, r, playURL)
		return
	}

	// 2. Account endpoint: /v1/account or /api/account
	if reqPath == "/v1/account" || reqPath == "/api/account" {
		handleAccountAPI(w, r)
		return
	}

	// 3. Public endpoints: /v1/packages, /health, /version
	if reqPath == "/v1/packages" || reqPath == "/api/packages" {
		proxyPublicEndpoint(w, "https://api.kineflex.site/v1/packages")
		return
	}
	if reqPath == "/health" || reqPath == "/api/health" {
		proxyPublicEndpoint(w, "https://api.kineflex.site/health")
		return
	}
	if reqPath == "/version" || reqPath == "/api/version" {
		proxyPublicEndpoint(w, "https://api.kineflex.site/version")
		return
	}

	// 4. TV routes: /api/tv/{id}/{season}/{episode} or /tv/{id}/{season}/{episode}
	if matches := apiTV.FindStringSubmatch(reqPath); len(matches) == 4 {
		handleTVAPI(w, r, matches[1], matches[2], matches[3])
		return
	}
	if matches := tvRegex.FindStringSubmatch(reqPath); len(matches) == 4 {
		handleTVPage(w, r, matches[1], matches[2], matches[3])
		return
	}

	// 5. Movie routes: /api/movie/{id} or /movie/{id}
	if matches := apiMovie.FindStringSubmatch(reqPath); len(matches) == 2 {
		handleMovieAPI(w, r, matches[1])
		return
	}
	if matches := movieRegex.FindStringSubmatch(reqPath); len(matches) == 2 {
		handleMoviePage(w, r, matches[1])
		return
	}

	// 6. Query parameter fallbacks (?play=... or ?url=...)
	if qPlay := r.URL.Query().Get("play"); qPlay != "" {
		handleDirectPlay(w, r, qPlay)
		return
	}
	if qURL := r.URL.Query().Get("url"); qURL != "" {
		handleDirectPlay(w, r, qURL)
		return
	}

	// 7. Root path: serve index.html if present
	if reqPath == "/" || reqPath == "" {
		if _, err := os.Stat("index.html"); err == nil {
			http.ServeFile(w, r, "index.html")
			return
		}
	}

	// 8. Serve static files if requested file exists on disk
	clean := filepath.Clean(strings.TrimPrefix(reqPath, "/"))
	if _, err := os.Stat(clean); err == nil && clean != "." {
		http.ServeFile(w, r, clean)
		return
	}

	// 9. Default: serve player.html
	servePlayerHTML(w, nil, nil)
}

// extractAndResolveKey extracts the API key from query params (?key=, ?api_key=, ?token=),
// Authorization header, X-API-Key header, or KINEFLEX_TOKEN env var,
// and decodes it if it was provided in base64.
func extractAndResolveKey(r *http.Request) (string, bool) {
	var rawKey string

	// 1. Check query parameters
	if k := r.URL.Query().Get("key"); k != "" {
		rawKey = k
	} else if k := r.URL.Query().Get("api_key"); k != "" {
		rawKey = k
	} else if k := r.URL.Query().Get("token"); k != "" {
		rawKey = k
	}

	// 2. Check Authorization header: Bearer <key>
	if rawKey == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			rawKey = strings.TrimSpace(authHeader[7:])
		}
	}

	// 3. Check X-API-Key header
	if rawKey == "" {
		rawKey = r.Header.Get("X-API-Key")
	}

	// 4. Fallback to environment variable if set
	if rawKey == "" {
		rawKey = os.Getenv("KINEFLEX_TOKEN")
	}

	rawKey = strings.TrimSpace(rawKey)
	if rawKey == "" {
		return "", false
	}

	resolved := resolveKey(rawKey)
	return resolved, true
}

// resolveKey decodes base64 if present, or returns the plaintext key
func resolveKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	// If already in common plaintext prefix format, return directly
	if strings.HasPrefix(raw, "kine_") {
		return raw
	}

	// Attempt base64 decoding (standard, url-safe, and raw unpadded variants)
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.RawURLEncoding,
	}

	for _, enc := range encodings {
		if decoded, err := enc.DecodeString(raw); err == nil && len(decoded) > 0 {
			str := string(decoded)
			if strings.HasPrefix(str, "kine_") {
				return str
			}
			if isCleanPrintableASCII(str) && (strings.Contains(raw, "=") || len(decoded) >= 8) {
				return str
			}
		}
	}

	return raw
}

func isCleanPrintableASCII(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 32 || s[i] > 126 {
			return false
		}
	}
	return true
}

// extractPlayURL extracts the target URL from /play=... in path or query
func extractPlayURL(reqPath, reqURI string, query url.Values) string {
	var target string

	if strings.HasPrefix(reqURI, "/play=") {
		target = strings.TrimPrefix(reqURI, "/play=")
	} else if strings.HasPrefix(reqPath, "/play=") {
		target = strings.TrimPrefix(reqPath, "/play=")
	} else if p := query.Get("play"); p != "" {
		target = p
	} else if u := query.Get("url"); u != "" {
		target = u
	}

	if target == "" {
		return ""
	}

	if decoded, err := url.QueryUnescape(target); err == nil && (strings.HasPrefix(decoded, "http://") || strings.HasPrefix(decoded, "https://")) {
		target = decoded
	}

	if strings.HasPrefix(target, "https:/") && !strings.HasPrefix(target, "https://") {
		target = strings.Replace(target, "https:/", "https://", 1)
	} else if strings.HasPrefix(target, "http:/") && !strings.HasPrefix(target, "http://") {
		target = strings.Replace(target, "http:/", "http://", 1)
	}

	return target
}

// handleDirectPlay serves player with direct stream URL
func handleDirectPlay(w http.ResponseWriter, r *http.Request, playURL string) {
	log.Printf("[Direct Play] URL: %s", playURL)

	title := r.URL.Query().Get("title")
	if title == "" {
		title = "Online Video Player"
	}

	payload := &MediaPayload{
		ID:    "direct",
		Title: title,
		URL:   playURL,
		Type:  "direct",
	}

	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(payload)
		return
	}

	servePlayerHTML(w, payload, nil)
}

// handleMoviePage fetches movie data from Kineflex and renders player or error modal
func handleMoviePage(w http.ResponseWriter, r *http.Request, id string) {
	log.Printf("[Movie] Fetching id: %s", id)

	key, keyProvided := extractAndResolveKey(r)
	if !keyProvided {
		errPayload := makeMissingKeyError()
		if wantsJSON(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   errPayload,
			})
			return
		}
		servePlayerHTML(w, nil, errPayload)
		return
	}

	data, errPayload := fetchMovie(id, key)
	if errPayload != nil {
		log.Printf("[Movie Error] ID %s: %s (%s)", id, errPayload.Code, errPayload.Message)
		if wantsJSON(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(errPayload.HTTPStatus)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   errPayload,
			})
			return
		}
		servePlayerHTML(w, nil, errPayload)
		return
	}

	payload := &MediaPayload{
		ID:     fmt.Sprintf("%v", data.ID),
		Title:  data.Name,
		URL:    data.URL,
		Poster: data.Poster,
		Type:   "movie",
	}

	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(payload)
		return
	}

	servePlayerHTML(w, payload, nil)
}

// handleMovieAPI returns JSON for movie endpoint
func handleMovieAPI(w http.ResponseWriter, r *http.Request, id string) {
	w.Header().Set("Content-Type", "application/json")

	key, keyProvided := extractAndResolveKey(r)
	if !keyProvided {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   makeMissingKeyError(),
		})
		return
	}

	data, errPayload := fetchMovie(id, key)
	if errPayload != nil {
		w.WriteHeader(errPayload.HTTPStatus)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   errPayload,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data":    data,
	})
}

// handleTVPage fetches TV show episode data and renders player or error modal
func handleTVPage(w http.ResponseWriter, r *http.Request, id, season, episode string) {
	log.Printf("[TV] Fetching show: %s season: %s episode: %s", id, season, episode)

	key, keyProvided := extractAndResolveKey(r)
	if !keyProvided {
		errPayload := makeMissingKeyError()
		if wantsJSON(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   errPayload,
			})
			return
		}
		servePlayerHTML(w, nil, errPayload)
		return
	}

	data, errPayload := fetchTV(id, season, episode, key)
	if errPayload != nil {
		log.Printf("[TV Error] %s S%sE%s: %s (%s)", id, season, episode, errPayload.Code, errPayload.Message)
		if wantsJSON(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(errPayload.HTTPStatus)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   errPayload,
			})
			return
		}
		servePlayerHTML(w, nil, errPayload)
		return
	}

	title := fmt.Sprintf("%s S%sE%s", data.Name, season, episode)
	payload := &MediaPayload{
		ID:      fmt.Sprintf("%v", data.ID),
		Title:   title,
		URL:     data.URL,
		Poster:  data.Poster,
		Type:    "tv",
		Season:  data.Season,
		Episode: data.Episode,
	}

	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(payload)
		return
	}

	servePlayerHTML(w, payload, nil)
}

// handleTVAPI returns JSON for TV endpoint
func handleTVAPI(w http.ResponseWriter, r *http.Request, id, season, episode string) {
	w.Header().Set("Content-Type", "application/json")

	key, keyProvided := extractAndResolveKey(r)
	if !keyProvided {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   makeMissingKeyError(),
		})
		return
	}

	data, errPayload := fetchTV(id, season, episode, key)
	if errPayload != nil {
		w.WriteHeader(errPayload.HTTPStatus)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   errPayload,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data":    data,
	})
}

// handleAccountAPI proxies the /v1/account endpoint to check balance
func handleAccountAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	key, keyProvided := extractAndResolveKey(r)
	if !keyProvided {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   makeMissingKeyError(),
		})
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "https://api.kineflex.site/v1/account", nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error": map[string]string{
				"code":    "INTERNAL_ERROR",
				"message": err.Error(),
			},
		})
		return
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error": map[string]string{
				"code":    "UPSTREAM_ERROR",
				"message": err.Error(),
			},
		})
		return
	}
	defer resp.Body.Close()

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// proxyPublicEndpoint proxies unauthenticated endpoints (packages, health, version)
func proxyPublicEndpoint(w http.ResponseWriter, targetURL string) {
	w.Header().Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(targetURL)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error": map[string]string{
				"code":    "UPSTREAM_ERROR",
				"message": err.Error(),
			},
		})
		return
	}
	defer resp.Body.Close()

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// fetchMovie calls Kineflex API (with fallback between endpoints)
func fetchMovie(id, key string) (*KineflexData, *ErrorPayload) {
	// 1. Try endpoint: https://api.kineflex.site/v1/tv/movie/{id}?web
	url1 := fmt.Sprintf("https://api.kineflex.site/v1/tv/movie/%s?web", id)
	data, errPayload := callKineflexAPI(url1, key)
	if errPayload == nil && data != nil && data.URL != "" {
		return data, nil
	}

	// If the error is fatal auth / insufficient points / rate limit, don't waste retry
	if errPayload != nil && (errPayload.HTTPStatus == 401 || errPayload.HTTPStatus == 402 || errPayload.HTTPStatus == 403 || errPayload.HTTPStatus == 429) {
		return nil, errPayload
	}

	// 2. Fallback to standard movie endpoint: https://api.kineflex.site/v1/movie/{id}?web
	url2 := fmt.Sprintf("https://api.kineflex.site/v1/movie/%s?web", id)
	data2, errPayload2 := callKineflexAPI(url2, key)
	if errPayload2 == nil && data2 != nil && data2.URL != "" {
		return data2, nil
	}

	if errPayload2 != nil {
		return nil, errPayload2
	}
	if errPayload != nil {
		return nil, errPayload
	}

	return nil, &ErrorPayload{
		Code:        "NOT_FOUND",
		Message:     "No stream URL found in API response for this title",
		HTTPStatus:  http.StatusNotFound,
		KeyProvided: true,
	}
}

// fetchTV calls Kineflex TV endpoint: https://api.kineflex.site/v1/tv/{id}/{season}/{episode}?web
func fetchTV(id, season, episode, key string) (*KineflexData, *ErrorPayload) {
	apiURL := fmt.Sprintf("https://api.kineflex.site/v1/tv/%s/%s/%s?web", id, season, episode)
	data, errPayload := callKineflexAPI(apiURL, key)
	if errPayload != nil {
		return nil, errPayload
	}
	if data == nil || data.URL == "" {
		return nil, &ErrorPayload{
			Code:        "NOT_FOUND",
			Message:     "No stream URL found in API response for this episode",
			HTTPStatus:  http.StatusNotFound,
			KeyProvided: true,
		}
	}
	return data, nil
}

// callKineflexAPI makes an authenticated HTTP request to Kineflex and parses success or errors
func callKineflexAPI(apiURL, key string) (*KineflexData, *ErrorPayload) {
	client := &http.Client{Timeout: 15 * time.Second}

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, &ErrorPayload{
			Code:        "INTERNAL_ERROR",
			Message:     fmt.Sprintf("Failed to construct request: %v", err),
			HTTPStatus:  http.StatusInternalServerError,
			KeyProvided: true,
		}
	}

	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("User-Agent", "KineflexGoWorker/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, &ErrorPayload{
			Code:        "UPSTREAM_ERROR",
			Message:     fmt.Sprintf("Failed to contact Kineflex API server: %v", err),
			HTTPStatus:  http.StatusBadGateway,
			KeyProvided: true,
		}
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &ErrorPayload{
			Code:        "INTERNAL_ERROR",
			Message:     "Failed to read API response body",
			HTTPStatus:  http.StatusInternalServerError,
			KeyProvided: true,
		}
	}

	retryAfter := resp.Header.Get("Retry-After")
	requestID := resp.Header.Get("X-Request-ID")

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var kResp KineflexResponse
		_ = json.Unmarshal(bodyBytes, &kResp)

		errPayload := &ErrorPayload{
			HTTPStatus:  resp.StatusCode,
			RetryAfter:  retryAfter,
			RequestID:   requestID,
			KeyProvided: true,
		}

		if kResp.Error != nil {
			errPayload.Code = kResp.Error.Code
			errPayload.Message = kResp.Error.Message
			errPayload.Required = kResp.Error.Required
			errPayload.Remaining = kResp.Error.Remaining
			errPayload.Field = kResp.Error.Field
		} else if kResp.Message != "" {
			errPayload.Message = kResp.Message
		}

		// Provide accurate fallbacks according to specification
		if errPayload.Code == "" {
			switch resp.StatusCode {
			case http.StatusUnauthorized: // 401
				errPayload.Code = "INVALID_API_KEY"
				errPayload.Message = "The provided API key does not exist or has been revoked."
			case http.StatusPaymentRequired: // 402
				errPayload.Code = "INSUFFICIENT_POINTS"
				errPayload.Message = "Account balance is below the request cost. Nothing was charged."
			case http.StatusForbidden: // 403
				errPayload.Code = "INSUFFICIENT_SCOPE"
				errPayload.Message = "The key lacks required scope or the account is suspended."
			case http.StatusNotFound: // 404
				errPayload.Code = "NOT_FOUND"
				errPayload.Message = "Video not found. No title with that ID (Points were refunded)."
			case http.StatusUnprocessableEntity: // 422
				errPayload.Code = "VALIDATION_ERROR"
				errPayload.Message = "A request parameter is missing or malformed."
			case http.StatusTooManyRequests: // 429
				errPayload.Code = "RATE_LIMITED"
				errPayload.Message = "Rate limit reached (60 requests/min). Rejected requests never cost points."
			case http.StatusBadGateway: // 502
				errPayload.Code = "UPSTREAM_ERROR"
				errPayload.Message = "API server or upstream video provider is currently busy. Points were returned."
			case http.StatusInternalServerError: // 500
				errPayload.Code = "INTERNAL_ERROR"
				errPayload.Message = "Something went wrong on the API server side."
			default:
				errPayload.Code = "API_ERROR"
				errPayload.Message = fmt.Sprintf("API returned HTTP %d: %s", resp.StatusCode, string(bodyBytes))
			}
		}

		return nil, errPayload
	}

	var res KineflexResponse
	if err := json.Unmarshal(bodyBytes, &res); err != nil {
		return nil, &ErrorPayload{
			Code:        "INTERNAL_ERROR",
			Message:     fmt.Sprintf("JSON parse error: %v (body: %s)", err, string(bodyBytes)),
			HTTPStatus:  http.StatusInternalServerError,
			KeyProvided: true,
		}
	}

	if !res.Success && !res.Status {
		msg := res.Message
		code := "API_ERROR"
		if res.Error != nil {
			if res.Error.Code != "" {
				code = res.Error.Code
			}
			if res.Error.Message != "" {
				msg = res.Error.Message
			}
		}
		if msg == "" {
			msg = "API response indicated error"
		}
		return nil, &ErrorPayload{
			Code:        code,
			Message:     msg,
			HTTPStatus:  resp.StatusCode,
			KeyProvided: true,
		}
	}

	return &res.Data, nil
}

func makeMissingKeyError() *ErrorPayload {
	return &ErrorPayload{
		Code:        "MISSING_API_KEY",
		Message:     "No API key was provided. Get your free API key at api.kineflex.site (free users get 100 points). If you are a developer, contact admin on Telegram: @lakshya9pro.",
		HTTPStatus:  http.StatusUnauthorized,
		KeyProvided: false,
	}
}

// servePlayerHTML reads player.html and injects MediaPayload or ErrorPayload into <head>
func servePlayerHTML(w http.ResponseWriter, payload *MediaPayload, errPayload *ErrorPayload) {
	htmlPath := "player.html"
	content, err := os.ReadFile(htmlPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error reading %s: %v", htmlPath, err), http.StatusInternalServerError)
		return
	}

	html := string(content)

	var scripts strings.Builder
	scripts.WriteString("\n  <script>\n")

	if payload != nil {
		jsonBytes, _ := json.Marshal(payload)
		scripts.WriteString(fmt.Sprintf("    window.initialMedia = %s;\n", string(jsonBytes)))

		if payload.Title != "" {
			titleRegex := regexp.MustCompile(`(?i)<title>.*?</title>`)
			if titleRegex.MatchString(html) {
				html = titleRegex.ReplaceAllString(html, fmt.Sprintf("<title>%s</title>", htmlEscape(payload.Title)))
			}
		}
	}

	if errPayload != nil {
		jsonBytes, _ := json.Marshal(errPayload)
		scripts.WriteString(fmt.Sprintf("    window.initialError = %s;\n", string(jsonBytes)))

		titleRegex := regexp.MustCompile(`(?i)<title>.*?</title>`)
		if titleRegex.MatchString(html) {
			displayTitle := errPayload.Code
			if errPayload.Code == "MISSING_API_KEY" {
				displayTitle = "API Key Required"
			} else if errPayload.Code == "NOT_FOUND" {
				displayTitle = "Video Not Found"
			} else if errPayload.Code == "UPSTREAM_ERROR" {
				displayTitle = "Server Busy"
			}
			html = titleRegex.ReplaceAllString(html, fmt.Sprintf("<title>%s - Kineflex</title>", htmlEscape(displayTitle)))
		}
	}

	scripts.WriteString("  </script>\n")

	if payload != nil || errPayload != nil {
		if strings.Contains(html, "<head>") {
			html = strings.Replace(html, "<head>", "<head>"+scripts.String(), 1)
		} else {
			html = scripts.String() + html
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(html))
}

func wantsJSON(r *http.Request) bool {
	if r.URL.Query().Get("format") == "json" || r.URL.Query().Get("json") == "true" {
		return true
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html")
}

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
