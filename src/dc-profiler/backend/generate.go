package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"google.golang.org/genai"
)

// maxFieldConcurrency bounds how many per-field Gemini calls run at once.
const maxFieldConcurrency = 5

// Suggestion is one generated field value paired with the current value so the
// UI can render them side-by-side.
type Suggestion struct {
	Key       string   `json:"key"`
	Label     string   `json:"label"`
	Kind      string   `json:"kind"`
	Current   string   `json:"current"`           // existing value (verbatim, as stored)
	Suggested string   `json:"suggested"`         // LLM suggestion (text/shorttext/enum)
	Tags      []string `json:"tags,omitempty"`    // for kind=="tags"/"enum": suggested slugs
	Options   []Option `json:"options,omitempty"` // allowed options for tags/enum
	MaxLen    int      `json:"maxLen,omitempty"`
}

// geminiModel is the Vertex AI model used for generation.
const geminiModel = "gemini-2.5-flash"

// generateSuggestions generates each catalog field with its OWN Gemini call,
// returning raw text per field rather than one big JSON object. This isolates
// fields — a runaway or truncated response in one field no longer corrupts the
// others — and removes the fragile "model must emit exact JSON with markdown
// escaped inside it" requirement. Fields are generated concurrently (bounded).
func generateSuggestions(ctx context.Context, dc *DataCollection, profile *DataProfile, prog progressFunc) ([]Suggestion, error) {
	client, err := newGenAIClient(ctx)
	if err != nil {
		return nil, err
	}
	return runFieldGeneration(ctx, client, dc, buildContext(dc, profile), prog), nil
}

// generateSuggestionsDeep is the extensive path: it runs the agentic investigation
// (which actively queries the data and searches the web) and then generates each
// field with the enriched context — the statistical profile plus the agent's
// findings and discovered external documentation links. It returns the
// investigation so the caller can surface it in the UI.
func generateSuggestionsDeep(ctx context.Context, dc *DataCollection, profile *DataProfile, deep *DeepProfile, prog progressFunc) ([]Suggestion, *Investigation, error) {
	client, err := newGenAIClient(ctx)
	if err != nil {
		return nil, nil, err
	}
	inv, err := runInvestigation(ctx, client, dc, profile, deep, prog)
	if err != nil {
		// Investigation failure shouldn't sink the whole run — fall back to the
		// statistical context without the dossier.
		logf("investigation failed, continuing without it: %v", err)
		inv = &Investigation{}
	}
	ctxText := buildDeepContext(dc, profile, deep, inv)
	suggs := runFieldGeneration(ctx, client, dc, ctxText, prog)
	// Fold the exact sampled statistics into the Data dictionary and Data
	// snapshot so the numbers live in saved fields (not just a transient UI).
	enrichDeepSuggestions(suggs, deep)
	return suggs, inv, nil
}

// runFieldGeneration generates every catalog field concurrently (bounded) from a
// shared context string and assembles the suggestions. It is the common core of
// the light and deep paths.
func runFieldGeneration(ctx context.Context, client *genai.Client, dc *DataCollection, contextText string, prog progressFunc) []Suggestion {
	total := len(Fields)
	prog(Progress{Phase: "generate", Message: fmt.Sprintf("Writing %d catalog fields…", total), Total: total})

	generated := make([]string, total)
	var done int32
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxFieldConcurrency)

	for i, f := range Fields {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, f Field) {
			defer wg.Done()
			defer func() { <-sem }()

			text, err := generateField(ctx, client, contextText, f, currentValue(dc, f))
			if err != nil {
				logf("field %q generation failed: %v", f.Key, err)
			}
			generated[i] = text

			n := atomic.AddInt32(&done, 1)
			msg := "Wrote " + f.Label
			if err != nil {
				msg = f.Label + " — skipped (" + err.Error() + ")"
			}
			prog(Progress{Phase: "generate", Message: msg, Current: int(n), Total: total})
		}(i, f)
	}
	wg.Wait()

	byKey := make(map[string]string, total)
	for i, f := range Fields {
		byKey[f.Key] = generated[i]
	}
	return assembleSuggestions(dc, byKey)
}

// generateField generates the value for a single field. It returns raw text
// (markdown for text fields, plain text or comma-separated slugs otherwise),
// with any accidental outer code-fence wrapper stripped.
func generateField(ctx context.Context, client *genai.Client, contextText string, f Field, current string) (string, error) {
	prompt := contextText + "\n== FIELD TO GENERATE ==\n" + fieldInstruction(f, current)
	config := &genai.GenerateContentConfig{
		Temperature: genai.Ptr[float32](0.3),
		// Per-field, so a generous ceiling is ample even for a big data
		// dictionary, while a truncation only affects this one field.
		MaxOutputTokens: genai.Ptr[int32](16384),
	}
	resp, err := client.Models.GenerateContent(ctx, geminiModel, genai.Text(prompt), config)
	if err != nil {
		return "", fmt.Errorf("gemini generate: %w", err)
	}
	return stripOuterFence(strings.TrimSpace(resp.Text())), nil
}

// newGenAIClient creates a Vertex AI GenAI client using the ambient project and
// the "global" location.
func newGenAIClient(ctx context.Context) (*genai.Client, error) {
	project := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if project == "" {
		project = os.Getenv("GCP_PROJECT_ID")
	}
	location := os.Getenv("GOOGLE_CLOUD_LOCATION")
	if location == "" {
		location = "global"
	}
	return genai.NewClient(ctx, &genai.ClientConfig{
		Backend:  genai.BackendVertexAI,
		Project:  project,
		Location: location,
	})
}

// buildContext assembles the shared context sent with every per-field request:
// the assistant role, global formatting rules, and the compact data profile.
func buildContext(dc *DataCollection, profile *DataProfile) string {
	var b strings.Builder
	b.WriteString("You are a data-catalog assistant for the Verily Workbench platform. ")
	b.WriteString("You are given a data collection and a profile of its underlying data assets ")
	b.WriteString("(BigQuery tables and Google Cloud Storage buckets). You generate high-quality, accurate ")
	b.WriteString("catalog metadata grounded ONLY in the provided data. ")
	b.WriteString("Do not invent facts that are not supported by the data profile. If the requested field ")
	b.WriteString("cannot be determined from the data, output an empty response.\n\n")

	b.WriteString("== MARKDOWN FORMATTING RULES (for markdown fields) ==\n")
	b.WriteString("- Use `##`/`###` headings and bullet or numbered lists to structure longer text.\n")
	b.WriteString("- Render a data dictionary / schema as a GFM pipe table with a header row, e.g.:\n")
	b.WriteString("  | Column | Type | Description |\n  | --- | --- | --- |\n  | id | INTEGER | Primary key |\n")
	b.WriteString("- Keep tables COMPACT: use exactly three dashes (`---`) in each separator cell. Do NOT pad cells, headers, or separator rows with extra spaces or repeated dashes for visual alignment.\n")
	b.WriteString("- Be concise; never repeat characters or content to fill space.\n")
	b.WriteString("- Put every SQL query in a fenced code block tagged `sql` (```sql … ```), each with a `-- comment` explaining it.\n")
	b.WriteString("- Use backticks for inline identifiers like `project.dataset.table` and column names.\n\n")

	// Each field is generated by an independent call that cannot see the others,
	// so spell out the division of labor to keep them from all restating the
	// schema. Only the Data dictionary owns the column-by-column breakdown.
	b.WriteString("== FIELD BOUNDARIES (each field has a distinct job — do not duplicate other fields) ==\n")
	b.WriteString("- Description: prose overview of what the data is, its provenance, and who would use it. Describe structure at a HIGH LEVEL only (e.g. \"one table keyed by country and year\"). Do NOT include a column-by-column list or schema table — that is the Data dictionary's job.\n")
	b.WriteString("- Data snapshot: the concrete assets and their sizes (tables, row counts, buckets, object counts). Not a schema.\n")
	b.WriteString("- Data dictionary: the ONLY field that lists every column with its type and description.\n")
	b.WriteString("- Data model: how tables relate (keys, grain, relationships) in prose. Not a column list.\n")
	b.WriteString("- Sample use cases / Sample SQL queries: examples only, no schema restatement.\n\n")

	b.WriteString("== DATA COLLECTION ==\n")
	fmt.Fprintf(&b, "Display name: %s\n", dc.DisplayName)
	fmt.Fprintf(&b, "User-facing ID: %s\n", dc.UserFacingID)
	if dc.Description != "" {
		fmt.Fprintf(&b, "Existing description:\n%s\n", dc.Description)
	}
	b.WriteString("\n== DATA PROFILE ==\n")
	b.WriteString(profileToText(profile))
	return b.String()
}

// buildDeepContext extends the shared context with everything the extensive
// (agentic) run produced: the statistical column profile, the investigation
// dossier, and the external documentation links the agent verified. These make
// the generated fields — especially References and External documentation —
// concrete and better grounded.
func buildDeepContext(dc *DataCollection, profile *DataProfile, deep *DeepProfile, inv *Investigation) string {
	var b strings.Builder
	b.WriteString(buildContext(dc, profile))

	b.WriteString("\n== STATISTICAL PROFILE (sampled) ==\n")
	b.WriteString(deepProfileToText(deep))

	if inv != nil {
		if d := strings.TrimSpace(inv.Dossier); d != "" {
			b.WriteString("\n== INVESTIGATION NOTES (from actively exploring the data and the web) ==\n")
			b.WriteString(d)
			b.WriteString("\n")
		}
		if len(inv.Links) > 0 {
			b.WriteString("\n== EXTERNAL DOCUMENTATION FOUND (verified links — cite these) ==\n")
			for _, l := range inv.Links {
				title := l.Title
				if title == "" {
					title = l.URL
				}
				fmt.Fprintf(&b, "- %s: %s\n", title, l.URL)
			}
		}
	}
	return b.String()
}

// fieldGuardrails holds extra per-field "stay in your lane" rules for the fields
// most prone to duplicating the schema. The column-by-column breakdown belongs
// ONLY to the Data dictionary; everything else references it at a high level.
var fieldGuardrails = map[string]string{
	"description":  "IMPORTANT: This is prose for a human, not a schema. Do NOT list columns or include any table of columns/types. Mention the structure only at a high level (e.g. \"a single table keyed by country and year\").\n",
	"dataSnapshot": "IMPORTANT: Report HIGH-LEVEL METRICS only — number of tables, row counts per table, bucket/object counts, total sizes, and the overall time/geographic span. Do NOT list individual columns or their types; that is the Data dictionary's job.\n",
	"dataModel":    "IMPORTANT: Describe relationships in prose (keys, grain, how tables join). Do NOT reproduce the full column list.\n",
}

// fieldInstruction is the per-field task appended to the shared context. It tells
// the model exactly what to write and in what raw form (plain text, markdown, or
// comma-separated slugs), with no JSON wrapper.
func fieldInstruction(f Field, current string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Field: %s\n", f.Label)
	fmt.Fprintf(&b, "What to write: %s\n", f.Description)
	if extra := fieldGuardrails[f.Key]; extra != "" {
		b.WriteString(extra)
	}
	if strings.TrimSpace(current) != "" {
		b.WriteString("\nCurrent value already stored for this field (may be outdated or incomplete):\n")
		b.WriteString("<<<\n" + strings.TrimSpace(current) + "\n>>>\n")
		b.WriteString("Reuse and improve it where it is accurate rather than discarding it. If it is already good and supported by the data, you may return it unchanged. Only replace parts that are wrong, outdated, or unsupported by the data profile.\n")
	}

	switch f.Kind {
	case "tags", "enum":
		var slugs []string
		for _, o := range f.Options {
			slugs = append(slugs, o.Value)
		}
		fmt.Fprintf(&b, "Respond with a comma-separated subset of exactly these allowed slugs (or an empty response if none apply): %s.\n",
			strings.Join(slugs, ", "))
		b.WriteString("Output ONLY the slugs, comma-separated. No prose, no markdown.\n")
	case "shorttext":
		b.WriteString("Output PLAIN TEXT only: a single line, no markdown, and no backticks around identifiers.\n")
		if f.MaxLen > 0 {
			fmt.Fprintf(&b, "Hard maximum length: %d characters.\n", f.MaxLen)
		}
	case "plaintext":
		b.WriteString("Output PLAIN TEXT only: no markdown, no headings, no tables, no bullet syntax, and no backticks — refer to identifiers by name in prose (e.g. Entity, not `Entity`).\n")
	default: // "text"
		b.WriteString("Format the value as GitHub-Flavored Markdown per the rules above.\n")
		b.WriteString("Use fully-qualified `project.dataset.table` names exactly as shown in the profile.\n")
	}
	b.WriteString("Output ONLY the field's value. Do not restate the field name, do not wrap the whole answer in quotes or a code fence, and do not emit JSON.\n")
	return b.String()
}

// stripOuterFence removes a code fence that wraps the ENTIRE answer (e.g. the
// model returned ```markdown … ```), which would otherwise render literally. It
// deliberately leaves content that contains multiple fenced blocks (like a set
// of ```sql queries) untouched.
func stripOuterFence(s string) string {
	if !strings.HasPrefix(s, "```") || !strings.HasSuffix(s, "```") {
		return s
	}
	if strings.Count(s, "```") != 2 {
		return s // multiple fenced blocks — not a single outer wrapper
	}
	nl := strings.IndexByte(s, '\n')
	if nl < 0 {
		return s
	}
	switch strings.TrimSpace(s[3:nl]) {
	case "", "markdown", "md", "text":
		inner := strings.TrimRight(s[nl+1:], " \t\n")
		inner = strings.TrimSuffix(inner, "```")
		return strings.TrimSpace(inner)
	default:
		return s // a real single code block (e.g. ```sql) — keep it
	}
}

// stripInlineMarkdown removes the light inline markdown a model tends to add even
// when asked for plain text, so values shown verbatim (the Data model / summary
// sidebar details) read cleanly. It unwraps `code`, **bold**, and *italic* spans
// while keeping their inner text; it does not touch multi-line structure.
func stripInlineMarkdown(s string) string {
	s = strings.ReplaceAll(s, "`", "")
	// Only asterisk emphasis is unwrapped; underscores are left alone so
	// snake_case identifiers (e.g. Carbon_Monoxide) are not mangled.
	for _, marker := range []string{"**", "*"} {
		s = unwrapEmphasis(s, marker)
	}
	return s
}

// unwrapEmphasis removes paired emphasis markers (e.g. ** or *) around spans,
// leaving the text between them. Unpaired markers are left as-is.
func unwrapEmphasis(s, marker string) string {
	var b strings.Builder
	rest := s
	for {
		i := strings.Index(rest, marker)
		if i < 0 {
			b.WriteString(rest)
			break
		}
		j := strings.Index(rest[i+len(marker):], marker)
		if j < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:i])
		b.WriteString(rest[i+len(marker) : i+len(marker)+j])
		rest = rest[i+len(marker)+j+len(marker):]
	}
	return b.String()
}

// profileToText renders the data profile compactly for the prompt.
func profileToText(p *DataProfile) string {
	var b strings.Builder
	if len(p.Tables) == 0 && len(p.Buckets) == 0 {
		b.WriteString("(No BigQuery or GCS data assets were accessible.)\n")
	}
	for _, t := range p.Tables {
		fmt.Fprintf(&b, "\nBigQuery table `%s` (%d rows)\n", t.FQN, t.NumRows)
		if t.Description != "" {
			fmt.Fprintf(&b, "  Table description: %s\n", t.Description)
		}
		b.WriteString("  Columns:\n")
		for _, c := range t.Columns {
			line := fmt.Sprintf("    - %s %s", c.Name, c.Type)
			if c.Description != "" {
				line += ": " + c.Description
			}
			b.WriteString(line + "\n")
		}
		if len(t.SampleRows) > 0 {
			b.WriteString("  Sample rows (JSON):\n")
			for _, row := range t.SampleRows {
				j, _ := json.Marshal(row)
				fmt.Fprintf(&b, "    %s\n", truncate(string(j), 400))
			}
		}
	}
	for _, bk := range p.Buckets {
		fmt.Fprintf(&b, "\nGCS bucket `%s`: %d objects, %d bytes total\n", bk.Bucket, bk.NumObjects, bk.TotalBytes)
		shown := bk.Objects
		if len(shown) > 30 {
			shown = shown[:30]
		}
		for _, o := range shown {
			fmt.Fprintf(&b, "    - %s (%d bytes, %s)\n", o.Name, o.Size, o.ContentType)
		}
		for _, pv := range bk.Previews {
			fmt.Fprintf(&b, "  Preview of %s:\n%s\n", pv.Name, indent(truncate(pv.Preview, 1500), "    "))
		}
	}
	if len(p.Errors) > 0 {
		b.WriteString("\nProfiling notes (some assets could not be read):\n")
		for _, e := range p.Errors {
			fmt.Fprintf(&b, "  - %s\n", e)
		}
	}
	return b.String()
}

// assembleSuggestions pairs each generated value with the current stored value.
func assembleSuggestions(dc *DataCollection, generated map[string]string) []Suggestion {
	var out []Suggestion
	for _, f := range Fields {
		gen := generated[f.Key]
		s := Suggestion{
			Key:     f.Key,
			Label:   f.Label,
			Kind:    f.Kind,
			Options: f.Options,
			MaxLen:  f.MaxLen,
		}
		s.Current = currentValue(dc, f)

		switch f.Kind {
		case "tags", "enum":
			s.Tags = parseSlugs(gen, f.Options)
			s.Suggested = strings.Join(s.Tags, ", ")
		default:
			v := strings.TrimSpace(gen)
			if f.Kind == "plaintext" || f.Kind == "shorttext" {
				// These render verbatim (no markdown parser), so strip any stray
				// inline markdown the model added out of habit — most commonly
				// backticks around identifiers.
				v = stripInlineMarkdown(v)
			}
			if f.MaxLen > 0 {
				// Hard backstop: Gemini treats the schema's maxLength as a hint
				// and sometimes overshoots, so guarantee the limit here.
				v = enforceMaxLen(v, f.MaxLen)
			}
			s.Suggested = v
		}
		out = append(out, s)
	}
	return out
}

// currentValue returns the DC's existing value for a field in display form.
func currentValue(dc *DataCollection, f Field) string {
	if f.IsDescription {
		return dc.Description
	}
	raw := dc.getProperty(f.PropertyKey)
	if f.JSONArray && raw != "" {
		var slugs []string
		if err := json.Unmarshal([]byte(raw), &slugs); err == nil {
			return strings.Join(slugs, ", ")
		}
	}
	return raw
}

// parseSlugs keeps only allowed slugs from a comma-separated list.
func parseSlugs(csv string, options []Option) []string {
	allowed := map[string]bool{}
	for _, o := range options {
		allowed[o.Value] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(csv, ",") {
		slug := strings.TrimSpace(part)
		if slug != "" && allowed[slug] && !seen[slug] {
			out = append(out, slug)
			seen[slug] = true
		}
	}
	return out
}

// enforceMaxLen trims s to at most max characters (counted as runes, matching
// the UI's character count for ASCII/BMP text), backing off to the last word
// boundary so it doesn't cut mid-word. Trailing punctuation is cleaned up.
func enforceMaxLen(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	cut := string(r[:max])
	// Back off to the last space to avoid a mid-word cut, unless that would
	// discard most of the text (e.g. one very long token).
	if i := strings.LastIndexAny(cut, " \t\n"); i > max/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " \t\n,;:-")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}
