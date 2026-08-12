package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// staticDir holds the built frontend (index.html + assets). Overridable via env
// for local dev.
var staticDir = envOr("STATIC_DIR", "./static")

func main() {
	jobs = newJobStore()

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
	parts := strings.Split(rest, "/")
	id := parts[0]
	if id == "" {
		httpError(w, http.StatusBadRequest, "missing data collection id")
		return
	}
	action := ""
	if len(parts) >= 2 {
		action = parts[1]
	}
	switch {
	case action == "":
		handleGetDC(w, r, id)
	case action == "suggest" && len(parts) == 2:
		handleStartSuggest(w, r, id)
	case action == "suggest" && len(parts) == 3:
		handlePollSuggest(w, r, parts[2])
	case action == "save" && len(parts) == 2:
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
	folders, err := listFolders(id)
	if err != nil {
		httpError(w, http.StatusBadGateway, "list folders: "+err.Error())
		return
	}
	versions, recommended := buildVersions(dc, folders, resources)
	writeJSON(w, http.StatusOK, map[string]any{
		"dataCollection":       dc,
		"resources":            resources,
		"fields":               Fields,
		"versions":             versions,
		"recommendedVersionId": recommended,
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

// handleStartSuggest kicks off a generation run in a detached goroutine and
// returns a job id immediately. The client then polls handlePollSuggest. This
// keeps every request short so the mesh proxy's route timeout never trips a
// long-running generation (which can take minutes).
func handleStartSuggest(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	deep := isDeepMode(r)
	versionID := r.URL.Query().Get("versionId")
	jobID, j := jobs.create()
	go runSuggestJob(id, deep, versionID, j)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID})
}

// handlePollSuggest returns the current state of a generation job: its status,
// accumulated progress steps, and the final result (or error) once terminal.
func handlePollSuggest(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	j, ok := jobs.get(jobID)
	if !ok {
		httpError(w, http.StatusNotFound, "unknown or expired job")
		return
	}
	writeJSON(w, http.StatusOK, j.snapshot())
}

// runSuggestJob runs the full profiling/generation pipeline, recording progress
// and the terminal result on the job. It runs detached from any HTTP request,
// bounded only by suggestTimeout.
func runSuggestJob(id string, deep bool, versionID string, j *job) {
	ctx, cancel := context.WithTimeout(context.Background(), suggestTimeout(deep))
	defer cancel()
	prog := func(p Progress) { j.addStep(p) }

	prog(Progress{Phase: "start", Message: "Loading data collection…"})
	dc, err := getDataCollection(id)
	if err != nil {
		j.finish(nil, fmt.Errorf("get data collection: %w", err))
		return
	}
	resources, err := listResources(id)
	if err != nil {
		j.finish(nil, fmt.Errorf("list resources: %w", err))
		return
	}
	folders, err := listFolders(id)
	if err != nil {
		j.finish(nil, fmt.Errorf("list folders: %w", err))
		return
	}

	// Scope profiling to a single version's resources. Metadata is DC-level, but
	// resources are per-version, so profiling all versions would mix them.
	_, recommended := buildVersions(dc, folders, resources)
	if versionID == "" {
		versionID = recommended
	}
	resources = filterResourcesByVersion(versionID, resources)
	if len(resources) == 0 {
		j.finish(nil, fmt.Errorf("selected version has no resources to profile"))
		return
	}

	profile := buildDataProfile(ctx, resources, prog)

	if deep {
		deepProfile := buildDeepProfile(ctx, profile, prog)
		suggestions, inv, err := generateSuggestionsDeep(ctx, dc, profile, deepProfile, prog)
		if err != nil {
			j.finish(nil, fmt.Errorf("generate suggestions: %w", err))
			return
		}
		j.finish(map[string]any{
			"suggestions":   suggestions,
			"profile":       profile,
			"deepProfile":   deepProfile,
			"investigation": inv,
		}, nil)
		return
	}

	suggestions, err := generateSuggestions(ctx, dc, profile, prog)
	if err != nil {
		j.finish(nil, fmt.Errorf("generate suggestions: %w", err))
		return
	}
	j.finish(map[string]any{
		"suggestions": suggestions,
		"profile":     profile,
	}, nil)
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
