package main

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/bigquery"
	"cloud.google.com/go/civil"
	"google.golang.org/api/iterator"
)

// Deep profiling computes real per-column statistics (fill rate, cardinality,
// numeric summaries, value distributions) the way industry profilers do. It is
// sample-based and cost-guarded: every query is dry-run first and skipped if it
// would scan more than deepMaxScanBytes, and large tables are sampled with
// TABLESAMPLE so only a fraction of blocks are read.
const (
	deepSampleTarget   = 50000            // aim to sample about this many rows
	deepMaxScanBytes   = int64(5) << 30   // 5 GiB dry-run ceiling per query
	deepMaxColumns     = 40               // cap columns profiled per table
	deepTopValueCols   = 6                // cap categorical columns given a top-N breakdown
	deepTopValueN      = 10               // top-N values per categorical column
	topValueMaxDistinct = 50             // only break down columns at or below this cardinality
	deepQueryTimeout   = 90 * time.Second // per-query budget
)

// DeepProfile holds statistical profiles for a data collection's tables plus any
// notes about assets that were skipped (e.g. too expensive to scan).
type DeepProfile struct {
	Tables []TableStats `json:"tables"`
	Notes  []string     `json:"notes,omitempty"`
}

// TableStats is the statistical profile of one table computed over a sample.
type TableStats struct {
	FQN         string        `json:"fqn"`
	SampledRows int64         `json:"sampledRows"`
	SamplePct   int           `json:"samplePct"` // 100 == full table
	Columns     []ColumnStats `json:"columns"`
}

// ColumnStats holds one column's computed statistics. Numeric/temporal summary
// fields are pointers so "not applicable" is distinguishable from a real zero.
type ColumnStats struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Category string  `json:"category"` // numeric | temporal | categorical | other
	NonNull  int64   `json:"nonNull"`
	Total    int64   `json:"total"`
	FillRate float64 `json:"fillRate"` // 0..1
	Distinct int64   `json:"distinct"` // approximate

	Min    *float64 `json:"min,omitempty"`
	Max    *float64 `json:"max,omitempty"`
	Mean   *float64 `json:"mean,omitempty"`
	Stddev *float64 `json:"stddev,omitempty"`
	// Approximate quartiles over the sample (25th / 50th (median) / 75th).
	P25    *float64 `json:"p25,omitempty"`
	Median *float64 `json:"median,omitempty"`
	P75    *float64 `json:"p75,omitempty"`

	TMin string `json:"tMin,omitempty"` // temporal min (rendered)
	TMax string `json:"tMax,omitempty"` // temporal max (rendered)

	TopValues []ValueCount `json:"topValues,omitempty"`
}

// ValueCount is one value and how often it appeared in the sample.
type ValueCount struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

// DocLink is an external documentation reference discovered during investigation.
type DocLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// buildDeepProfile computes statistical profiles for every BigQuery table already
// discovered in the (light) DataProfile, reusing its schema and row counts so no
// metadata is fetched twice. GCS assets have no columnar statistics, so they are
// left to the light profile.
func buildDeepProfile(ctx context.Context, profile *DataProfile, prog progressFunc) *DeepProfile {
	dp := &DeepProfile{}
	if profile == nil {
		return dp
	}
	// Group tables by project so we reuse one BigQuery client per project.
	clients := map[string]*bigquery.Client{}
	defer func() {
		for _, c := range clients {
			c.Close()
		}
	}()

	for _, t := range profile.Tables {
		if t.Project == "" || len(t.Columns) == 0 {
			continue
		}
		client := clients[t.Project]
		if client == nil {
			c, err := bigquery.NewClient(ctx, t.Project)
			if err != nil {
				dp.Notes = append(dp.Notes, fmt.Sprintf("%s: cannot open BigQuery client: %v", t.FQN, err))
				continue
			}
			clients[t.Project] = c
			client = c
		}
		prog(Progress{Phase: "deepprofile", Message: fmt.Sprintf("Computing column statistics for %s…", t.Table)})
		ts, err := profileTableStats(ctx, client, t)
		if err != nil {
			dp.Notes = append(dp.Notes, fmt.Sprintf("%s: %v", t.FQN, err))
			continue
		}
		if ts == nil {
			dp.Notes = append(dp.Notes, fmt.Sprintf("%s: skipped (would scan more than %s)", t.FQN, humanBytes(deepMaxScanBytes)))
			continue
		}
		prog(Progress{Phase: "deepprofile", Message: fmt.Sprintf("  %s — %d cols over %s sampled rows", t.Table, len(ts.Columns), humanCount(uint64(ts.SampledRows)))})
		dp.Tables = append(dp.Tables, *ts)
	}
	return dp
}

// statCol pairs a schema column with its analysis category.
type statCol struct {
	name     string
	typ      string
	category string
}

// profileTableStats runs one aggregate query over a sample of the table to get
// per-column fill/cardinality/range stats, then a bounded set of top-value
// queries for low-cardinality categorical columns. Returns nil (no error) when
// the table is skipped by the cost guard.
func profileTableStats(ctx context.Context, client *bigquery.Client, t TableProfile) (*TableStats, error) {
	cols := analyzableColumns(t.Columns)
	if len(cols) == 0 {
		return nil, fmt.Errorf("no analyzable columns")
	}

	pct := samplePercent(t.NumRows)
	from := fmt.Sprintf("`%s`", t.FQN)
	if pct < 100 {
		from += fmt.Sprintf(" TABLESAMPLE SYSTEM (%d PERCENT)", pct)
	}

	agg := buildAggregateSQL(cols, from)
	row, err := runSingleRow(ctx, client, agg)
	if err != nil {
		return nil, fmt.Errorf("aggregate query: %w", err)
	}
	if row == nil {
		return nil, nil // cost-guarded skip
	}

	ts := &TableStats{FQN: t.FQN, SamplePct: pct}
	ts.SampledRows = asInt(row["n_total"])

	for i, c := range cols {
		cs := ColumnStats{Name: c.name, Type: c.typ, Category: c.category, Total: ts.SampledRows}
		cs.NonNull = ts.SampledRows - asInt(row[fmt.Sprintf("nl_%d", i)])
		if ts.SampledRows > 0 {
			cs.FillRate = float64(cs.NonNull) / float64(ts.SampledRows)
		}
		cs.Distinct = asInt(row[fmt.Sprintf("ds_%d", i)])
		switch c.category {
		case "numeric":
			cs.Min = asFloatPtr(row[fmt.Sprintf("mn_%d", i)])
			cs.Max = asFloatPtr(row[fmt.Sprintf("mx_%d", i)])
			cs.Mean = asFloatPtr(row[fmt.Sprintf("av_%d", i)])
			cs.Stddev = asFloatPtr(row[fmt.Sprintf("sd_%d", i)])
			cs.P25 = asFloatPtr(row[fmt.Sprintf("q1_%d", i)])
			cs.Median = asFloatPtr(row[fmt.Sprintf("md_%d", i)])
			cs.P75 = asFloatPtr(row[fmt.Sprintf("q3_%d", i)])
		case "temporal":
			cs.TMin = renderValue(row[fmt.Sprintf("mn_%d", i)])
			cs.TMax = renderValue(row[fmt.Sprintf("mx_%d", i)])
		}
		ts.Columns = append(ts.Columns, cs)
	}

	addTopValues(ctx, client, from, ts)
	return ts, nil
}

// addTopValues fills TopValues for up to deepTopValueCols categorical columns that
// have low cardinality, one bounded GROUP BY query per column.
func addTopValues(ctx context.Context, client *bigquery.Client, from string, ts *TableStats) {
	budget := deepTopValueCols
	for i := range ts.Columns {
		if budget == 0 {
			break
		}
		cs := &ts.Columns[i]
		if cs.Category != "categorical" || cs.Distinct == 0 || cs.Distinct > topValueMaxDistinct {
			continue
		}
		sql := fmt.Sprintf(
			"SELECT CAST(`%s` AS STRING) AS v, COUNT(*) AS c FROM %s WHERE `%s` IS NOT NULL GROUP BY v ORDER BY c DESC LIMIT %d",
			cs.Name, from, cs.Name, deepTopValueN)
		rows, err := runRows(ctx, client, sql)
		if err != nil {
			continue // best-effort
		}
		for _, r := range rows {
			cs.TopValues = append(cs.TopValues, ValueCount{Value: renderValue(r["v"]), Count: asInt(r["c"])})
		}
		budget--
	}
}

// analyzableColumns keeps top-level scalar columns and assigns each a category.
// Nested/repeated/complex columns are skipped (they can't be aggregated simply).
func analyzableColumns(cols []ColumnInfo) []statCol {
	var out []statCol
	for _, c := range cols {
		if len(out) >= deepMaxColumns {
			break
		}
		if c.Mode == "REPEATED" {
			continue
		}
		cat := categorize(c.Type)
		if cat == "" {
			continue
		}
		out = append(out, statCol{name: c.Name, typ: c.Type, category: cat})
	}
	return out
}

// categorize maps a BigQuery type to an analysis category, or "" to skip it.
func categorize(t string) string {
	switch strings.ToUpper(t) {
	case "INTEGER", "INT64", "FLOAT", "FLOAT64", "NUMERIC", "BIGNUMERIC":
		return "numeric"
	case "DATE", "DATETIME", "TIMESTAMP", "TIME":
		return "temporal"
	case "STRING", "BOOL", "BOOLEAN":
		return "categorical"
	default: // RECORD/STRUCT, BYTES, GEOGRAPHY, JSON, INTERVAL
		return ""
	}
}

// buildAggregateSQL assembles the single-pass stats query. Every column gets a
// null-count and distinct-count; numeric and temporal columns also get min/max
// (plus avg/stddev for numeric).
func buildAggregateSQL(cols []statCol, from string) string {
	var sel []string
	sel = append(sel, "COUNT(*) AS n_total")
	for i, c := range cols {
		sel = append(sel, fmt.Sprintf("COUNTIF(`%s` IS NULL) AS nl_%d", c.name, i))
		sel = append(sel, fmt.Sprintf("APPROX_COUNT_DISTINCT(`%s`) AS ds_%d", c.name, i))
		switch c.category {
		case "numeric":
			sel = append(sel, fmt.Sprintf("MIN(`%s`) AS mn_%d", c.name, i))
			sel = append(sel, fmt.Sprintf("MAX(`%s`) AS mx_%d", c.name, i))
			sel = append(sel, fmt.Sprintf("AVG(CAST(`%s` AS FLOAT64)) AS av_%d", c.name, i))
			sel = append(sel, fmt.Sprintf("STDDEV(CAST(`%s` AS FLOAT64)) AS sd_%d", c.name, i))
			// APPROX_QUANTILES(x, 4) -> [min, q1, median, q3, max]; index the
			// interior three inline so we get scalar quartiles (nulls ignored).
			q := fmt.Sprintf("APPROX_QUANTILES(CAST(`%s` AS FLOAT64), 4)", c.name)
			sel = append(sel, fmt.Sprintf("%s[SAFE_OFFSET(1)] AS q1_%d", q, i))
			sel = append(sel, fmt.Sprintf("%s[SAFE_OFFSET(2)] AS md_%d", q, i))
			sel = append(sel, fmt.Sprintf("%s[SAFE_OFFSET(3)] AS q3_%d", q, i))
		case "temporal":
			sel = append(sel, fmt.Sprintf("CAST(MIN(`%s`) AS STRING) AS mn_%d", c.name, i))
			sel = append(sel, fmt.Sprintf("CAST(MAX(`%s`) AS STRING) AS mx_%d", c.name, i))
		}
	}
	return "SELECT " + strings.Join(sel, ", ") + " FROM " + from
}

// samplePercent returns the TABLESAMPLE percent (1..100) to target roughly
// deepSampleTarget rows; 100 means scan the whole (small) table.
func samplePercent(numRows uint64) int {
	if numRows == 0 || numRows <= deepSampleTarget {
		return 100
	}
	p := int(math.Ceil(100 * float64(deepSampleTarget) / float64(numRows)))
	if p < 1 {
		p = 1
	}
	if p > 100 {
		p = 100
	}
	return p
}

// runSingleRow runs an aggregate query (after the dry-run cost guard) and returns
// its single result row, or nil if the cost guard tripped.
func runSingleRow(ctx context.Context, client *bigquery.Client, sql string) (map[string]bigquery.Value, error) {
	rows, err := runRows(ctx, client, sql)
	if err != nil || rows == nil {
		return nil, err
	}
	if len(rows) == 0 {
		return map[string]bigquery.Value{}, nil
	}
	return rows[0], nil
}

// runRows runs a read-only query and returns its rows, first dry-running it and
// returning (nil, nil) if it would scan more than deepMaxScanBytes.
func runRows(ctx context.Context, client *bigquery.Client, sql string) ([]map[string]bigquery.Value, error) {
	over, bytes, err := exceedsCostGuard(ctx, client, sql)
	if err != nil {
		return nil, err
	}
	if over {
		logf("deep profile: skipping query (~%s > %s): %s", humanBytes(bytes), humanBytes(deepMaxScanBytes), truncate(sql, 120))
		return nil, nil
	}

	qctx, cancel := context.WithTimeout(ctx, deepQueryTimeout)
	defer cancel()
	q := client.Query(sql)
	it, err := q.Read(qctx)
	if err != nil {
		return nil, err
	}
	var out []map[string]bigquery.Value
	for {
		var row map[string]bigquery.Value
		err := it.Next(&row)
		if err == iterator.Done {
			break
		}
		if err != nil {
			return out, err
		}
		out = append(out, row)
	}
	return out, nil
}

// exceedsCostGuard dry-runs a query and reports whether its estimated scan
// exceeds the ceiling, along with the estimate.
func exceedsCostGuard(ctx context.Context, client *bigquery.Client, sql string) (bool, int64, error) {
	q := client.Query(sql)
	q.DryRun = true
	job, err := q.Run(ctx)
	if err != nil {
		return false, 0, err
	}
	var bytes int64
	if st := job.LastStatus(); st != nil && st.Statistics != nil {
		bytes = st.Statistics.TotalBytesProcessed
	}
	return bytes > deepMaxScanBytes, bytes, nil
}

// --- BigQuery value coercion helpers ---

func asInt(v bigquery.Value) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	default:
		return 0
	}
}

func asFloatPtr(v bigquery.Value) *float64 {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case float64:
		return &t
	case int64:
		f := float64(t)
		return &f
	case int:
		f := float64(t)
		return &f
	case *big.Rat:
		f, _ := t.Float64()
		return &f
	default:
		return nil
	}
}

// renderValue formats a BigQuery scalar for display (dates/times to short forms).
func renderValue(v bigquery.Value) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
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
		return trimFloat(f)
	case float64:
		return trimFloat(t)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%.4f", f)
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
}

// sortColumnsByFill is a small helper the UI could use; kept here so ordering is
// consistent if we ever pre-sort. Currently unused by the pipeline.
func sortColumnsByFill(cs []ColumnStats) {
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].FillRate < cs[j].FillRate })
}
