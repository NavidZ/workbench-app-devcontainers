package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// staticDir holds the built frontend (index.html + assets). Overridable via env
// for local dev.
var staticDir = envOr("STATIC_DIR", "./static")

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", handleHealth)
	mux.HandleFunc("/api/data-collections", handleListDCs)
	mux.HandleFunc("/api/data-collections/", handleDCSubroutes) // /{id}, /{id}/suggest, /{id}/save
	mux.HandleFunc("/", handleStatic)

	addr := ":" + envOr("PORT", "8080")
	log.Printf("dc-metadata-autofill listening on %s", addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

// handleDCSubroutes dispatches /api/data-collections/{id}[/suggest|/save].
func handleDCSubroutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/data-collections/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	if id == "" {
		httpError(w, http.StatusBadRequest, "missing data collection id")
		return
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	switch action {
	case "":
		handleGetDC(w, r, id)
	case "suggest":
		handleSuggest(w, r, id)
	case "save":
		handleSave(w, r, id)
	default:
		httpError(w, http.StatusNotFound, "unknown action")
	}
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleListDCs returns all DCs the caller can write to.
func handleListDCs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	dcs, err := listDataCollections()
	if err != nil {
		httpError(w, http.StatusBadGateway, "list data collections: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dataCollections": dcs})
}

// handleGetDC returns a single DC with its resources.
func handleGetDC(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	dc, err := getDataCollection(id)
	if err != nil {
		httpError(w, http.StatusBadGateway, "get data collection: "+err.Error())
		return
	}
	resources, err := listResources(id)
	if err != nil {
		httpError(w, http.StatusBadGateway, "list resources: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dataCollection": dc,
		"resources":      resources,
		"fields":         Fields,
	})
}

// Progress is one step in the suggestion pipeline, streamed to the UI over SSE
// so the user sees live progress instead of an opaque spinner.
type Progress struct {
	Phase   string `json:"phase"`
	Message string `json:"message"`
	Current int    `json:"current,omitempty"`
	Total   int    `json:"total,omitempty"`
}

// progressFunc receives progress updates during profiling/generation.
type progressFunc func(Progress)

// noProgress is a no-op progress sink for callers that don't stream.
func noProgress(Progress) {}

// handleSuggest inspects the DC's data and returns LLM suggestions side-by-side
// with current values. GET streams live progress over SSE (ending with a
// `result` event); POST returns the same payload as a single JSON response.
func handleSuggest(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method == http.MethodGet {
		handleSuggestStream(w, r, id)
		return
	}
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "GET or POST only")
		return
	}
	deep := isDeepMode(r)
	ctx, cancel := context.WithTimeout(r.Context(), suggestTimeout(deep))
	defer cancel()

	dc, err := getDataCollection(id)
	if err != nil {
		httpError(w, http.StatusBadGateway, "get data collection: "+err.Error())
		return
	}
	resources, err := listResources(id)
	if err != nil {
		httpError(w, http.StatusBadGateway, "list resources: "+err.Error())
		return
	}
	profile := buildDataProfile(ctx, resources, noProgress)

	payload := map[string]any{"profile": profile}
	if deep {
		deepProfile := buildDeepProfile(ctx, profile, noProgress)
		suggestions, inv, err := generateSuggestionsDeep(ctx, dc, profile, deepProfile, noProgress)
		if err != nil {
			httpError(w, http.StatusBadGateway, "generate suggestions: "+err.Error())
			return
		}
		payload["suggestions"] = suggestions
		payload["deepProfile"] = deepProfile
		payload["investigation"] = inv
	} else {
		suggestions, err := generateSuggestions(ctx, dc, profile, noProgress)
		if err != nil {
			httpError(w, http.StatusBadGateway, "generate suggestions: "+err.Error())
			return
		}
		payload["suggestions"] = suggestions
	}
	writeJSON(w, http.StatusOK, payload)
}

// isDeepMode reports whether the caller requested the extensive (agentic) path.
func isDeepMode(r *http.Request) bool {
	return r.URL.Query().Get("mode") == "deep"
}

// suggestTimeout gives the agentic path more headroom for tool use.
func suggestTimeout(deep bool) time.Duration {
	if deep {
		return 12 * time.Minute
	}
	return 5 * time.Minute
}

// handleSuggestStream runs the same pipeline as handleSuggest but streams
// progress events (`event: progress`) as it goes, then a final `event: result`
// with the suggestions + profile, or `event: fail` on error.
func handleSuggestStream(w http.ResponseWriter, r *http.Request, id string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Per-field generation calls prog (→ send) from multiple goroutines, so the
	// SSE writes must be serialized.
	var sendMu sync.Mutex
	send := func(event string, v any) {
		b, _ := json.Marshal(v)
		sendMu.Lock()
		defer sendMu.Unlock()
		_, _ = w.Write([]byte("event: " + event + "\ndata: "))
		_, _ = w.Write(b)
		_, _ = w.Write([]byte("\n\n"))
		flusher.Flush()
	}
	fail := func(msg string) { send("fail", map[string]string{"message": msg}) }

	deep := isDeepMode(r)
	ctx, cancel := context.WithTimeout(r.Context(), suggestTimeout(deep))
	defer cancel()

	send("progress", Progress{Phase: "start", Message: "Loading data collection…"})
	dc, err := getDataCollection(id)
	if err != nil {
		fail("get data collection: " + err.Error())
		return
	}
	resources, err := listResources(id)
	if err != nil {
		fail("list resources: " + err.Error())
		return
	}

	prog := func(p Progress) { send("progress", p) }
	profile := buildDataProfile(ctx, resources, prog)

	if deep {
		deepProfile := buildDeepProfile(ctx, profile, prog)
		suggestions, inv, err := generateSuggestionsDeep(ctx, dc, profile, deepProfile, prog)
		if err != nil {
			fail("generate suggestions: " + err.Error())
			return
		}
		send("result", map[string]any{
			"suggestions":   suggestions,
			"profile":       profile,
			"deepProfile":   deepProfile,
			"investigation": inv,
		})
		return
	}

	suggestions, err := generateSuggestions(ctx, dc, profile, prog)
	if err != nil {
		fail("generate suggestions: " + err.Error())
		return
	}
	send("result", map[string]any{
		"suggestions": suggestions,
		"profile":     profile,
	})
}

// saveRequest is the payload from the review UI: the user-approved values per
// field key. Tag/enum fields send a slug array in Tags; other fields send Value.
type saveRequest struct {
	Fields []struct {
		Key   string   `json:"key"`
		Value string   `json:"value"`
		Tags  []string `json:"tags"`
	} `json:"fields"`
}

// handleSave persists the approved values: description via PATCH, everything
// else via the properties endpoint.
func handleSave(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req saveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}

	var props []Property
	var saved []string
	for _, f := range req.Fields {
		field := FieldByKey(f.Key)
		if field == nil {
			continue
		}
		if field.IsDescription {
			if err := updateDescription(id, f.Value); err != nil {
				httpError(w, http.StatusBadGateway, "save description: "+err.Error())
				return
			}
			saved = append(saved, f.Key)
			continue
		}
		value := f.Value
		if field.JSONArray {
			slugs := f.Tags
			if slugs == nil {
				slugs = []string{}
			}
			b, _ := json.Marshal(slugs)
			value = string(b)
		} else if field.Kind == "enum" && len(f.Tags) > 0 {
			value = f.Tags[0]
		}
		props = append(props, Property{Key: field.PropertyKey, Value: value})
		saved = append(saved, f.Key)
	}
	if err := setProperties(id, props); err != nil {
		httpError(w, http.StatusBadGateway, "save properties: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": saved})
}

// handleStatic serves the built SPA, falling back to index.html for client-side
// routes.
func handleStatic(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		httpError(w, http.StatusNotFound, "not found")
		return
	}
	clean := filepath.Clean(r.URL.Path)
	path := filepath.Join(staticDir, clean)
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		http.ServeFile(w, r, path)
		return
	}
	http.ServeFile(w, r, filepath.Join(staticDir, "index.html"))
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, status int, msg string) {
	log.Printf("HTTP %d: %s", status, msg)
	writeJSON(w, status, map[string]string{"error": msg})
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s (%s)", r.Method, r.URL.Path, time.Since(start))
	})
}

func logf(format string, args ...any) { log.Printf(format, args...) }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
