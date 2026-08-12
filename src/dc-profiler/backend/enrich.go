package main

import (
	"fmt"
	"regexp"
	"strings"
)

// enrich.go folds the deterministic statistical profile into the two catalog
// fields where it belongs — the Data dictionary (per-column stats) and the Data
// snapshot (a coverage overview). This is what makes the deep profile *land in
// saved fields*: the numbers the user reviews and saves are exact (taken
// straight from DeepProfile), not paraphrased by the model. It runs only on the
// extensive path; the light path never touches these.

// enrichDeepSuggestions rewrites the dataDictionary and dataSnapshot suggestions
// in place, weaving the sampled statistics into their markdown.
func enrichDeepSuggestions(suggs []Suggestion, deep *DeepProfile) {
	if deep == nil || len(deep.Tables) == 0 {
		return
	}
	for i := range suggs {
		switch suggs[i].Key {
		case "dataDictionary":
			suggs[i].Suggested = enrichDataDictionary(suggs[i].Suggested, deep)
		case "dataSnapshot":
			suggs[i].Suggested = strings.TrimRight(suggs[i].Suggested, "\n") + coverageOverview(deep)
		}
	}
}

// enrichDataDictionary injects two authoritative columns — "Fill %" and
// "Distinct / values" — into each GFM data-dictionary table the model produced,
// matching rows to profiled columns by name. The model's own descriptions are
// preserved; only the hard numbers come from the profile. If a table can't be
// parsed or a column isn't profiled, that part is left untouched, so this never
// makes the dictionary worse than the model's output.
func enrichDataDictionary(md string, deep *DeepProfile) string {
	if strings.TrimSpace(md) == "" {
		return md
	}

	// Column stats keyed by lowercased name: a per-table map (selected by the
	// nearest "###" heading) plus a global fallback for unmatched headings.
	global := map[string]ColumnStats{}
	perTable := map[string]map[string]ColumnStats{}
	for _, t := range deep.Tables {
		m := map[string]ColumnStats{}
		for _, c := range t.Columns {
			key := strings.ToLower(c.Name)
			m[key] = c
			global[key] = c
		}
		perTable[strings.ToLower(shortTableName(t.FQN))] = m
	}

	lines := strings.Split(md, "\n")
	out := make([]string, 0, len(lines)+4)
	cur := global

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// A heading switches which table's stats we resolve against.
		if h := headingText(line); h != "" {
			cur = global
			hl := strings.ToLower(h)
			for name, m := range perTable {
				if strings.Contains(hl, name) {
					cur = m
					break
				}
			}
			out = append(out, line)
			continue
		}

		// A dictionary header row followed by a separator row starts a table.
		if typeIdx := dictTypeIndex(line); typeIdx >= 0 && i+1 < len(lines) && isSeparatorRow(lines[i+1]) {
			out = append(out, insertCells(line, typeIdx, []string{"Fill %", "Distinct / values"}))
			out = append(out, insertCells(lines[i+1], typeIdx, []string{"---", "---"}))
			i++ // consumed the separator

			for i+1 < len(lines) && isTableRow(lines[i+1]) {
				i++
				cells := splitRow(lines[i])
				fill, vals := "—", "—"
				if len(cells) > 0 {
					if c, ok := cur[strings.ToLower(cleanIdent(cells[0]))]; ok {
						fill = fmt.Sprintf("%.0f%%", c.FillRate*100)
						vals = valuesCell(c)
					} else if c, ok := global[strings.ToLower(cleanIdent(cells[0]))]; ok {
						fill = fmt.Sprintf("%.0f%%", c.FillRate*100)
						vals = valuesCell(c)
					}
				}
				out = append(out, insertCells(lines[i], typeIdx, []string{fill, vals}))
			}
			continue
		}

		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// valuesCell renders the compact "what values are in here" cell: approximate
// cardinality plus a range (numeric/temporal) or the top categories.
func valuesCell(c ColumnStats) string {
	parts := []string{fmt.Sprintf("~%s distinct", humanCount(uint64(max(c.Distinct, 0))))}
	switch c.Category {
	case "numeric":
		if c.Min != nil && c.Max != nil {
			parts = append(parts, fmt.Sprintf("%s–%s", trimFloat(*c.Min), trimFloat(*c.Max)))
		}
	case "temporal":
		if c.TMin != "" || c.TMax != "" {
			parts = append(parts, fmt.Sprintf("%s–%s", c.TMin, c.TMax))
		}
	default:
		if len(c.TopValues) > 0 {
			tops := make([]string, 0, 3)
			for _, tv := range c.TopValues {
				if len(tops) == 3 {
					break
				}
				v := tv.Value
				if strings.TrimSpace(v) == "" {
					v = "(blank)"
				}
				tops = append(tops, v)
			}
			parts = append(parts, "top: "+strings.Join(tops, ", "))
		}
	}
	return escapeCell(strings.Join(parts, "; "))
}

// coverageOverview is the deterministic block appended to the Data snapshot: a
// per-table completeness summary, which is exactly the "what's up with the
// data" overview the snapshot is meant to give.
func coverageOverview(deep *DeepProfile) string {
	var b strings.Builder
	b.WriteString("\n\n### Data coverage\n")
	for _, t := range deep.Tables {
		var full, sparse []string
		for _, c := range t.Columns {
			switch {
			case c.FillRate >= 0.999:
				full = append(full, c.Name)
			case c.FillRate < 0.75:
				sparse = append(sparse, fmt.Sprintf("%s (%.0f%%)", c.Name, c.FillRate*100))
			}
		}
		fmt.Fprintf(&b, "**`%s`** — %s rows profiled across %d columns",
			shortTableName(t.FQN), humanCount(uint64(max(t.SampledRows, 0))), len(t.Columns))
		if t.SamplePct > 0 && t.SamplePct < 100 {
			fmt.Fprintf(&b, " (%d%% sample)", t.SamplePct)
		}
		b.WriteString(".\n")
		if len(full) > 0 {
			fmt.Fprintf(&b, "- Fully populated: %s\n", strings.Join(capList(full, 8), ", "))
		}
		if len(sparse) > 0 {
			fmt.Fprintf(&b, "- Sparsely populated: %s\n", strings.Join(capList(sparse, 6), ", "))
		}
	}
	return b.String()
}

// --- small markdown-table helpers -------------------------------------------

var separatorCellRe = regexp.MustCompile(`^:?-{1,}:?$`)

// headingText returns the text of a markdown ATX heading (## / ###), or "".
func headingText(line string) string {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "#") {
		return ""
	}
	return strings.TrimSpace(strings.TrimLeft(s, "# "))
}

// isTableRow reports whether a line is a GFM table row (pipe-delimited).
func isTableRow(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "|")
}

// splitRow returns the trimmed content cells of a pipe row, dropping the empty
// cells produced by the leading/trailing border pipes.
func splitRow(line string) []string {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "|")
	s = strings.TrimSuffix(s, "|")
	parts := strings.Split(s, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// isSeparatorRow reports whether a line is a table's header/body separator
// (cells made only of dashes/colons).
func isSeparatorRow(line string) bool {
	if !isTableRow(line) {
		return false
	}
	cells := splitRow(line)
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		if !separatorCellRe.MatchString(c) {
			return false
		}
	}
	return true
}

// dictTypeIndex returns the index of the "Type" cell in a dictionary header row
// (one that also has a "Column" cell), or -1 if the line isn't such a header.
func dictTypeIndex(line string) int {
	if !isTableRow(line) {
		return -1
	}
	cells := splitRow(line)
	hasColumn, typeIdx := false, -1
	for i, c := range cells {
		lc := strings.ToLower(c)
		if strings.Contains(lc, "column") {
			hasColumn = true
		}
		if lc == "type" && typeIdx < 0 {
			typeIdx = i
		}
	}
	if hasColumn && typeIdx >= 0 {
		return typeIdx
	}
	return -1
}

// insertCells rebuilds a pipe row with the given cells inserted immediately
// after position afterIdx (0-based among content cells).
func insertCells(line string, afterIdx int, extra []string) string {
	cells := splitRow(line)
	if afterIdx < 0 || afterIdx >= len(cells) {
		afterIdx = len(cells) - 1
	}
	merged := make([]string, 0, len(cells)+len(extra))
	merged = append(merged, cells[:afterIdx+1]...)
	merged = append(merged, extra...)
	merged = append(merged, cells[afterIdx+1:]...)
	return "| " + strings.Join(merged, " | ") + " |"
}

// cleanIdent strips markdown decoration (backticks, emphasis) and spaces from a
// cell so a column name can be matched.
func cleanIdent(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`*_ ")
	return strings.TrimSpace(s)
}

// escapeCell escapes pipes and collapses newlines so a value can't break the
// surrounding table.
func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.ReplaceAll(s, "|", `\|`)
}

// shortTableName returns the final segment of a `project.dataset.table` FQN.
func shortTableName(fqn string) string {
	if i := strings.LastIndex(fqn, "."); i >= 0 {
		return fqn[i+1:]
	}
	return fqn
}

// capList truncates a list to n entries, appending an ellipsis marker.
func capList(items []string, n int) []string {
	if len(items) <= n {
		return items
	}
	return append(items[:n:n], fmt.Sprintf("…(+%d more)", len(items)-n))
}
