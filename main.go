package main

import (
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
	defaultToken = "kine_live_9AuUd4Y1uqQ-GGACHQGNBIxn2E4oxm7H"
	bearerToken  = defaultToken
	defaultPort  = "8080"
)

// Kineflex API response structure
type KineflexResponse struct {
	Success bool         `json:"success"`
	Status  bool         `json:"status"`
	Message string       `json:"message,omitempty"`
	Data    KineflexData `json:"data"`
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

var (
	movieRegex = regexp.MustCompile(`^/movie/([^/?#]+)`)
	tvRegex    = regexp.MustCompile(`^/tv/([^/?#]+)/([^/?#]+)/([^/?#]+)`)
	apiMovie   = regexp.MustCompile(`^/api/movie/([^/?#]+)`)
	apiTV      = regexp.MustCompile(`^/api/tv/([^/?#]+)/([^/?#]+)/([^/?#]+)`)
)

func main() {
	if envToken := os.Getenv("KINEFLEX_TOKEN"); envToken != "" {
		bearerToken = envToken
	}

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
	log.Printf("  - Movie stream:   http://localhost:%s/movie/550", port)
	log.Printf("  - TV stream:      http://localhost:%s/tv/259909/2/2", port)
	log.Printf("  - Direct stream:  http://localhost:%s/play=<videourl>", port)
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
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Range")

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

	// 1. Check for /play=... direct stream
	playURL := extractPlayURL(reqPath, reqURI, r.URL.Query())
	if playURL != "" {
		handleDirectPlay(w, r, playURL)
		return
	}

	// 2. Check for TV route: /tv/{id}/{season}/{episode} or /api/tv/{id}/{season}/{episode}
	if matches := apiTV.FindStringSubmatch(reqPath); len(matches) == 4 {
		handleTVAPI(w, matches[1], matches[2], matches[3])
		return
	}
	if matches := tvRegex.FindStringSubmatch(reqPath); len(matches) == 4 {
		handleTVPage(w, r, matches[1], matches[2], matches[3])
		return
	}

	// 3. Check for Movie route: /movie/{id} or /api/movie/{id}
	if matches := apiMovie.FindStringSubmatch(reqPath); len(matches) == 2 {
		handleMovieAPI(w, matches[1])
		return
	}
	if matches := movieRegex.FindStringSubmatch(reqPath); len(matches) == 2 {
		handleMoviePage(w, r, matches[1])
		return
	}

	// 4. Check for query parameters (?play=... or ?url=...)
	if qPlay := r.URL.Query().Get("play"); qPlay != "" {
		handleDirectPlay(w, r, qPlay)
		return
	}
	if qURL := r.URL.Query().Get("url"); qURL != "" {
		handleDirectPlay(w, r, qURL)
		return
	}

	// 5. Serve static files if requested file exists
	if reqPath != "/" {
		clean := filepath.Clean(strings.TrimPrefix(reqPath, "/"))
		if _, err := os.Stat(clean); err == nil && clean != "." {
			http.ServeFile(w, r, clean)
			return
		}
	}

	// 6. Default: serve player.html
	servePlayerHTML(w, nil)
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

	// Unescape if needed
	if decoded, err := url.QueryUnescape(target); err == nil && (strings.HasPrefix(decoded, "http://") || strings.HasPrefix(decoded, "https://")) {
		target = decoded
	}

	// Fix potential single slash after scheme if collapsed by proxy
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

	servePlayerHTML(w, payload)
}

// handleMoviePage fetches movie data from Kineflex and renders player
func handleMoviePage(w http.ResponseWriter, r *http.Request, id string) {
	log.Printf("[Movie] Fetching id: %s", id)

	data, err := fetchMovie(id)
	if err != nil {
		log.Printf("[Movie Error] ID %s: %v", id, err)
		http.Error(w, fmt.Sprintf("Failed to fetch movie ID %s: %v", id, err), http.StatusBadGateway)
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

	servePlayerHTML(w, payload)
}

// handleMovieAPI returns JSON for movie endpoint
func handleMovieAPI(w http.ResponseWriter, id string) {
	data, err := fetchMovie(id)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

// handleTVPage fetches TV show episode data and renders player
func handleTVPage(w http.ResponseWriter, r *http.Request, id, season, episode string) {
	log.Printf("[TV] Fetching show: %s season: %s episode: %s", id, season, episode)

	data, err := fetchTV(id, season, episode)
	if err != nil {
		log.Printf("[TV Error] %s S%sE%s: %v", id, season, episode, err)
		http.Error(w, fmt.Sprintf("Failed to fetch TV %s S%sE%s: %v", id, season, episode, err), http.StatusBadGateway)
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

	servePlayerHTML(w, payload)
}

// handleTVAPI returns JSON for TV endpoint
func handleTVAPI(w http.ResponseWriter, id, season, episode string) {
	data, err := fetchTV(id, season, episode)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

// fetchMovie calls Kineflex API (with fallback between endpoints)
func fetchMovie(id string) (*KineflexData, error) {
	// First try user-specified endpoint: https://api.kineflex.site/v1/tv/movie/{id}?web
	url1 := fmt.Sprintf("https://api.kineflex.site/v1/tv/movie/%s?web", id)
	data, err := callKineflexAPI(url1)
	if err == nil && data != nil && data.URL != "" {
		return data, nil
	}

	// Fallback to standard movie endpoint: https://api.kineflex.site/v1/movie/{id}?web
	url2 := fmt.Sprintf("https://api.kineflex.site/v1/movie/%s?web", id)
	data2, err2 := callKineflexAPI(url2)
	if err2 == nil && data2 != nil && data2.URL != "" {
		return data2, nil
	}

	if err2 != nil {
		return nil, err2
	}
	return nil, fmt.Errorf("no stream URL found in API response")
}

// fetchTV calls Kineflex TV endpoint: https://api.kineflex.site/v1/tv/{id}/{season}/{episode}?web
func fetchTV(id, season, episode string) (*KineflexData, error) {
	apiURL := fmt.Sprintf("https://api.kineflex.site/v1/tv/%s/%s/%s?web", id, season, episode)
	data, err := callKineflexAPI(apiURL)
	if err != nil {
		return nil, err
	}
	if data.URL == "" {
		return nil, fmt.Errorf("no stream URL found in API response")
	}
	return data, nil
}

// callKineflexAPI makes an authenticated HTTP request to Kineflex
func callKineflexAPI(apiURL string) (*KineflexData, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("User-Agent", "KineflexGoWorker/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API returned HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var res KineflexResponse
	if err := json.Unmarshal(bodyBytes, &res); err != nil {
		return nil, fmt.Errorf("JSON parse error: %w (body: %s)", err, string(bodyBytes))
	}

	if !res.Success && !res.Status && res.Message != "" {
		return nil, fmt.Errorf("API error: %s", res.Message)
	}

	return &res.Data, nil
}

// servePlayerHTML reads player.html and injects MediaPayload into <head>
func servePlayerHTML(w http.ResponseWriter, payload *MediaPayload) {
	htmlPath := "player.html"
	content, err := os.ReadFile(htmlPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error reading %s: %v", htmlPath, err), http.StatusInternalServerError)
		return
	}

	html := string(content)

	if payload != nil {
		jsonBytes, _ := json.Marshal(payload)
		injection := fmt.Sprintf("\n  <script>\n    window.initialMedia = %s;\n  </script>\n", string(jsonBytes))

		// Replace <title>...</title> if title exists
		if payload.Title != "" {
			titleRegex := regexp.MustCompile(`(?i)<title>.*?</title>`)
			if titleRegex.MatchString(html) {
				html = titleRegex.ReplaceAllString(html, fmt.Sprintf("<title>%s</title>", htmlEscape(payload.Title)))
			}
		}

		// Inject window.initialMedia into <head>
		if strings.Contains(html, "<head>") {
			html = strings.Replace(html, "<head>", "<head>"+injection, 1)
		} else {
			html = injection + html
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
