package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"cloud.google.com/go/bigquery"
	"cloud.google.com/go/civil"
	"cloud.google.com/go/storage"
	"google.golang.org/genai"
)

// The agentic investigation loop lets Gemini actively explore a data collection
// via read-only tools (query BigQuery, read GCS files, search/fetch the web)
// rather than reasoning over a single fixed profile. It ends when the model calls
// submit_findings, returning a Markdown dossier plus discovered external links,
// which then enrich the per-field metadata generation.
const (
	maxAgentSteps      = 16
	agentMaxRowsPerQ   = 200
	agentMaxToolChars  = 12000 // cap tool output fed back to the model
	webFetchMaxBytes   = 300 * 1024
	webFetchTimeout    = 20 * time.Second
	webSearchTimeout   = 45 * time.Second
)

// Investigation is the agent's output: free-form findings and the external
// documentation links it discovered.
type Investigation struct {
	Dossier string    `json:"dossier"`
	Links   []DocLink `json:"links,omitempty"`
}

// investigator holds the clients and context for one investigation run.
type investigator struct {
	ctx            context.Context
	genai          *genai.Client
	bq             *bigquery.Client
	gcs            *storage.Client
	billingProject string
	prog           progressFunc
	step           int
}

// runInvestigation drives the tool-use loop and returns the agent's findings.
// On any fatal setup error it returns the error; tool-level failures are fed back
// to the model as error strings so it can adapt.
func runInvestigation(ctx context.Context, client *genai.Client, dc *DataCollection, profile *DataProfile, deep *DeepProfile, prog progressFunc) (*Investigation, error) {
	iv := &investigator{ctx: ctx, genai: client, prog: prog, billingProject: billingProjectFor(profile)}
	defer iv.close()

	prog(Progress{Phase: "investigate", Message: "Starting data investigation…"})

	config := &genai.GenerateContentConfig{
		Temperature:       genai.Ptr[float32](0.4),
		MaxOutputTokens:   genai.Ptr[int32](8192),
		SystemInstruction: genai.NewContentFromParts([]*genai.Part{genai.NewPartFromText(agentSystemPrompt)}, "user"),
		Tools:             []*genai.Tool{{FunctionDeclarations: agentTools()}},
	}

	// We manage the conversation history manually rather than using the genai
	// Chat helper: in v0.7.0 the Chat helper strips non-text parts (including
	// function calls) from recorded model turns, which corrupts a tool-use loop.
	// Kick off with the data context the light profile already assembled, plus a
	// compact statistical summary so the agent knows where to dig.
	kickoff := buildContext(dc, profile) + "\n\n== STATISTICAL PROFILE ==\n" + deepProfileToText(deep) +
		"\n\nInvestigate this data collection using your tools, then call submit_findings."
	contents := []*genai.Content{
		genai.NewContentFromParts([]*genai.Part{genai.NewPartFromText(kickoff)}, "user"),
	}

	for iv.step = 0; iv.step < maxAgentSteps; iv.step++ {
		resp, err := iv.genai.Models.GenerateContent(ctx, geminiModel, contents, config)
		if err != nil {
			return nil, fmt.Errorf("investigation step %d: %w", iv.step, err)
		}
		// Record the model's turn verbatim (function-call parts intact).
		if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
			contents = append(contents, resp.Candidates[0].Content)
		}

		calls := resp.FunctionCalls()
		if len(calls) == 0 {
			// Model replied with prose instead of calling submit_findings; take the
			// text as the dossier.
			if txt := strings.TrimSpace(resp.Text()); txt != "" {
				return &Investigation{Dossier: txt}, nil
			}
			return &Investigation{Dossier: "(no findings produced)"}, nil
		}

		var replies []*genai.Part
		for _, call := range calls {
			if call.Name == "submit_findings" {
				return parseFindings(call.Args), nil
			}
			out := iv.dispatch(call)
			replies = append(replies, genai.NewPartFromFunctionResponse(call.Name, map[string]any{"result": out}))
		}
		contents = append(contents, genai.NewContentFromParts(replies, "user"))
	}

	// Ran out of steps: ask once more for a final summary.
	prog(Progress{Phase: "investigate", Message: "Wrapping up investigation…"})
	contents = append(contents, genai.NewContentFromParts([]*genai.Part{genai.NewPartFromText(
		"You have reached the investigation step limit. Summarize your findings now and call submit_findings.")}, "user"))
	if final, err := iv.genai.Models.GenerateContent(ctx, geminiModel, contents, config); err == nil {
		for _, call := range final.FunctionCalls() {
			if call.Name == "submit_findings" {
				return parseFindings(call.Args), nil
			}
		}
		if txt := strings.TrimSpace(final.Text()); txt != "" {
			return &Investigation{Dossier: txt}, nil
		}
	}
	return &Investigation{Dossier: "(investigation ended without a summary)"}, nil
}

// dispatch runs one tool call and returns its result string (already size-capped).
func (iv *investigator) dispatch(call *genai.FunctionCall) string {
	switch call.Name {
	case "bq_query":
		return iv.toolBQQuery(argStr(call.Args, "sql"))
	case "gcs_read":
		return iv.toolGCSRead(argStr(call.Args, "bucket"), argStr(call.Args, "object"), argInt(call.Args, "maxBytes", 4096))
	case "web_search":
		return iv.toolWebSearch(argStr(call.Args, "query"))
	case "web_fetch":
		return iv.toolWebFetch(argStr(call.Args, "url"))
	default:
		return "error: unknown tool " + call.Name
	}
}

// --- tools ---

func (iv *investigator) toolBQQuery(sql string) string {
	sql = strings.TrimSpace(sql)
	if sql == "" {
		return "error: empty sql"
	}
	iv.report("🔎 bq_query", truncate(sql, 160))
	client, err := iv.bqClient()
	if err != nil {
		return "error: " + err.Error()
	}
	rows, err := runRows(iv.ctx, client, sql)
	if err != nil {
		return "error: " + err.Error()
	}
	if rows == nil {
		return fmt.Sprintf("skipped: query would scan more than %s; add filters or use APPROX aggregates over a smaller column set", humanBytes(deepMaxScanBytes))
	}
	if len(rows) > agentMaxRowsPerQ {
		rows = rows[:agentMaxRowsPerQ]
	}
	safe := make([]map[string]any, len(rows))
	for i, r := range rows {
		safe[i] = jsonSafeRow(r)
	}
	b, _ := json.Marshal(safe)
	return capStr(fmt.Sprintf("%d rows:\n%s", len(rows), string(b)))
}

func (iv *investigator) toolGCSRead(bucket, object string, maxBytes int) string {
	if bucket == "" || object == "" {
		return "error: bucket and object are required"
	}
	if maxBytes <= 0 || maxBytes > webFetchMaxBytes {
		maxBytes = 8192
	}
	iv.report("📄 gcs_read", fmt.Sprintf("gs://%s/%s", bucket, object))
	client, err := iv.gcsClient()
	if err != nil {
		return "error: " + err.Error()
	}
	rctx, cancel := context.WithTimeout(iv.ctx, webFetchTimeout)
	defer cancel()
	r, err := client.Bucket(bucket).Object(object).NewRangeReader(rctx, 0, int64(maxBytes))
	if err != nil {
		return "error: " + err.Error()
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		return "error: " + err.Error()
	}
	return capStr(string(data))
}

func (iv *investigator) toolWebSearch(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return "error: empty query"
	}
	iv.report("🌐 web_search", query)
	sctx, cancel := context.WithTimeout(iv.ctx, webSearchTimeout)
	defer cancel()
	cfg := &genai.GenerateContentConfig{
		Temperature: genai.Ptr[float32](0.2),
		Tools:       []*genai.Tool{{GoogleSearch: &genai.GoogleSearch{}}},
	}
	resp, err := iv.genai.Models.GenerateContent(sctx, geminiModel, genai.Text(
		"Search the web and answer concisely, citing authoritative sources: "+query), cfg)
	if err != nil {
		return "error: web search unavailable: " + err.Error()
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(resp.Text()))
	for _, src := range groundingLinks(resp) {
		fmt.Fprintf(&b, "\nSOURCE: %s — %s", src.Title, src.URL)
	}
	return capStr(b.String())
}

func (iv *investigator) toolWebFetch(url string) string {
	url = strings.TrimSpace(url)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "error: url must start with http:// or https://"
	}
	iv.report("🌐 web_fetch", url)
	fctx, cancel := context.WithTimeout(iv.ctx, webFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(fctx, http.MethodGet, url, nil)
	if err != nil {
		return "error: " + err.Error()
	}
	req.Header.Set("User-Agent", "dc-metadata-autofill/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "error: " + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Sprintf("error: HTTP %d", resp.StatusCode)
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxBytes))
	return capStr(stripHTML(string(data)))
}

// --- client helpers ---

func (iv *investigator) bqClient() (*bigquery.Client, error) {
	if iv.bq != nil {
		return iv.bq, nil
	}
	if iv.billingProject == "" {
		return nil, fmt.Errorf("no billing project available for BigQuery")
	}
	c, err := bigquery.NewClient(iv.ctx, iv.billingProject)
	if err != nil {
		return nil, err
	}
	iv.bq = c
	return c, nil
}

func (iv *investigator) gcsClient() (*storage.Client, error) {
	if iv.gcs != nil {
		return iv.gcs, nil
	}
	c, err := storage.NewClient(iv.ctx)
	if err != nil {
		return nil, err
	}
	iv.gcs = c
	return c, nil
}

func (iv *investigator) close() {
	if iv.bq != nil {
		iv.bq.Close()
	}
	if iv.gcs != nil {
		iv.gcs.Close()
	}
}

func (iv *investigator) report(tool, detail string) {
	iv.prog(Progress{
		Phase:   "investigate",
		Message: fmt.Sprintf("%s %s", tool, detail),
		Current: iv.step + 1,
		Total:   maxAgentSteps,
	})
}

// billingProjectFor picks a project to bill queries to: the first table's
// project, else the ambient GOOGLE_CLOUD_PROJECT.
func billingProjectFor(profile *DataProfile) string {
	if profile != nil {
		for _, t := range profile.Tables {
			if t.Project != "" {
				return t.Project
			}
		}
	}
	if p := os.Getenv("GOOGLE_CLOUD_PROJECT"); p != "" {
		return p
	}
	return os.Getenv("GCP_PROJECT_ID")
}

// --- tool declarations ---

func agentTools() []*genai.FunctionDeclaration {
	strProp := func(desc string) *genai.Schema {
		return &genai.Schema{Type: genai.TypeString, Description: desc}
	}
	return []*genai.FunctionDeclaration{
		{
			Name:        "bq_query",
			Description: "Run a read-only BigQuery Standard SQL query against the data collection's tables (reference them by fully-qualified `project.dataset.table`). Use for profiling: null/fill rates, distinct counts, distributions, ranges, cross-tabs, examples. Queries are cost-guarded (rejected if they would scan too much) — prefer APPROX_ aggregates and avoid SELECT * on large tables.",
			Parameters: &genai.Schema{
				Type:       genai.TypeObject,
				Properties: map[string]*genai.Schema{"sql": strProp("The BigQuery Standard SQL SELECT statement.")},
				Required:   []string{"sql"},
			},
		},
		{
			Name:        "gcs_read",
			Description: "Read the first bytes of an object in a Google Cloud Storage bucket belonging to the data collection. Use to inspect file headers, CSV/JSON samples, or README/docs files.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"bucket":   strProp("Bucket name (no gs:// prefix)."),
					"object":   strProp("Object path within the bucket."),
					"maxBytes": {Type: genai.TypeInteger, Description: "Max bytes to read (default 8192)."},
				},
				Required: []string{"bucket", "object"},
			},
		},
		{
			Name:        "web_search",
			Description: "Search the public web for authoritative information about this dataset: its publisher, methodology, official documentation, data dictionaries, licensing, and update cadence. Returns a summary plus source URLs. Use these URLs as external documentation links.",
			Parameters: &genai.Schema{
				Type:       genai.TypeObject,
				Properties: map[string]*genai.Schema{"query": strProp("What to search for.")},
				Required:   []string{"query"},
			},
		},
		{
			Name:        "web_fetch",
			Description: "Fetch a specific web page (e.g. an official documentation URL found via web_search) and return its readable text, so you can extract precise details and confirm links before citing them.",
			Parameters: &genai.Schema{
				Type:       genai.TypeObject,
				Properties: map[string]*genai.Schema{"url": strProp("Absolute http(s) URL to fetch.")},
				Required:   []string{"url"},
			},
		},
		{
			Name:        "submit_findings",
			Description: "Call this once when your investigation is complete to submit your findings. Provide a thorough Markdown dossier and any authoritative external documentation links you verified.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"dossier": strProp("Markdown summary of everything you learned: what the data is, provenance, structure, notable statistics, data-quality observations, and how it should be described in the catalog."),
					"links": {
						Type:        genai.TypeArray,
						Description: "External documentation links you found and verified.",
						Items: &genai.Schema{
							Type: genai.TypeObject,
							Properties: map[string]*genai.Schema{
								"title": strProp("Human-readable title of the resource."),
								"url":   strProp("Absolute URL."),
							},
							Required: []string{"title", "url"},
						},
					},
				},
				Required: []string{"dossier"},
			},
		},
	}
}

const agentSystemPrompt = `You are a meticulous data-catalog analyst for the Verily Workbench platform.
Your job is to investigate a data collection's actual data and its public context, then hand back
findings that will be used to write high-quality catalog metadata.

Work like an analyst:
- Use bq_query to measure the data directly: fill/null rates, distinct counts and cardinality,
  value distributions, numeric ranges, date coverage, and relationships between tables.
- Use gcs_read to inspect files (headers, samples, READMEs) when the collection has buckets.
- Use web_search and web_fetch to identify the dataset's publisher, official documentation,
  data dictionaries, methodology, licensing, and update cadence — and collect authoritative links.
- Ground every claim in evidence you actually observed. Do not invent facts.

Be efficient: a handful of well-chosen queries and searches is better than many redundant ones.
When you have enough to describe the data collection well, call submit_findings exactly once with a
thorough Markdown dossier and the external links you verified.`

// --- parsing / formatting helpers ---

func parseFindings(args map[string]any) *Investigation {
	inv := &Investigation{Dossier: strings.TrimSpace(argStr(args, "dossier"))}
	if raw, ok := args["links"].([]any); ok {
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			title, _ := m["title"].(string)
			url, _ := m["url"].(string)
			if strings.TrimSpace(url) == "" {
				continue
			}
			inv.Links = append(inv.Links, DocLink{Title: strings.TrimSpace(title), URL: strings.TrimSpace(url)})
		}
	}
	if inv.Dossier == "" {
		inv.Dossier = "(no dossier text provided)"
	}
	return inv
}

// groundingLinks extracts web source links from a grounded response.
func groundingLinks(resp *genai.GenerateContentResponse) []DocLink {
	var out []DocLink
	seen := map[string]bool{}
	for _, cand := range resp.Candidates {
		if cand.GroundingMetadata == nil {
			continue
		}
		for _, ch := range cand.GroundingMetadata.GroundingChunks {
			if ch.Web == nil || ch.Web.URI == "" || seen[ch.Web.URI] {
				continue
			}
			seen[ch.Web.URI] = true
			out = append(out, DocLink{Title: ch.Web.Title, URL: ch.Web.URI})
		}
	}
	return out
}

// deepProfileToText renders the statistical profile compactly for the prompt.
func deepProfileToText(dp *DeepProfile) string {
	if dp == nil || len(dp.Tables) == 0 {
		return "(no statistical profile available)\n"
	}
	var b strings.Builder
	for _, t := range dp.Tables {
		fmt.Fprintf(&b, "\nTable `%s` (sampled %s rows, %d%% sample):\n", t.FQN, humanCount(uint64(t.SampledRows)), t.SamplePct)
		for _, c := range t.Columns {
			fmt.Fprintf(&b, "  - %s (%s): fill %.0f%%, ~%d distinct", c.Name, c.Type, c.FillRate*100, c.Distinct)
			switch c.Category {
			case "numeric":
				if c.Min != nil && c.Max != nil {
					fmt.Fprintf(&b, ", range %s..%s", trimFloat(*c.Min), trimFloat(*c.Max))
				}
				if c.Mean != nil {
					fmt.Fprintf(&b, ", mean %s", trimFloat(*c.Mean))
				}
				if c.Stddev != nil {
					fmt.Fprintf(&b, ", stddev %s", trimFloat(*c.Stddev))
				}
				if c.P25 != nil && c.Median != nil && c.P75 != nil {
					fmt.Fprintf(&b, ", quartiles %s/%s/%s (p25/median/p75)", trimFloat(*c.P25), trimFloat(*c.Median), trimFloat(*c.P75))
				} else if c.Median != nil {
					fmt.Fprintf(&b, ", median %s", trimFloat(*c.Median))
				}
			case "temporal":
				if c.TMin != "" || c.TMax != "" {
					fmt.Fprintf(&b, ", %s..%s", c.TMin, c.TMax)
				}
			}
			if len(c.TopValues) > 0 {
				var tops []string
				for _, tv := range c.TopValues {
					tops = append(tops, fmt.Sprintf("%s(%d)", tv.Value, tv.Count))
				}
				fmt.Fprintf(&b, ", top: %s", strings.Join(tops, ", "))
			}
			b.WriteString("\n")
		}
	}
	if len(dp.Notes) > 0 {
		b.WriteString("\nNotes:\n")
		for _, n := range dp.Notes {
			fmt.Fprintf(&b, "  - %s\n", n)
		}
	}
	return b.String()
}

// jsonSafeRow converts a BigQuery result row to JSON-marshalable values.
func jsonSafeRow(r map[string]bigquery.Value) map[string]any {
	out := make(map[string]any, len(r))
	for k, v := range r {
		out[k] = jsonSafe(v)
	}
	return out
}

func jsonSafe(v bigquery.Value) any {
	switch t := v.(type) {
	case nil, string, bool, int64, float64:
		return t
	case time.Time:
		return t.Format(time.RFC3339)
	case civil.Date:
		return t.String()
	case civil.DateTime:
		return t.String()
	case civil.Time:
		return t.String()
	case *big.Rat:
		f, _ := t.Float64()
		return f
	case []byte:
		return fmt.Sprintf("<%d bytes>", len(t))
	default:
		return fmt.Sprintf("%v", v)
	}
}

var htmlTagRe = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</\s*(script|style)\s*>|<[^>]+>`)
var wsRe = regexp.MustCompile(`[ \t]*\n\s*\n\s*`)

// stripHTML removes tags and script/style blocks and collapses whitespace so a
// fetched page becomes readable text for the model.
func stripHTML(s string) string {
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = wsRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func capStr(s string) string {
	if len(s) <= agentMaxToolChars {
		return s
	}
	return s[:agentMaxToolChars] + "\n…(truncated)"
}

func argStr(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func argInt(args map[string]any, key string, def int) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int64:
		return int(v)
	case int:
		return v
	default:
		return def
	}
}
