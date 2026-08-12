package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/bigquery"
	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

// DataProfile is a compact, LLM-friendly summary of a data collection's
// underlying data assets. It is also returned to the UI so the user can see
// what the suggestions were based on.
type DataProfile struct {
	Tables  []TableProfile  `json:"tables"`
	Buckets []BucketProfile `json:"buckets"`
	Errors  []string        `json:"errors,omitempty"`
}

// TableProfile summarizes a single BigQuery table.
type TableProfile struct {
	FQN         string         `json:"fqn"` // project.dataset.table
	Project     string         `json:"project"`
	Dataset     string         `json:"dataset"`
	Table       string         `json:"table"`
	Description string         `json:"description,omitempty"`
	NumRows     uint64         `json:"numRows"`
	SizeBytes   int64          `json:"sizeBytes"`
	Columns     []ColumnInfo   `json:"columns"`
	SampleRows  []map[string]any `json:"sampleRows,omitempty"`
}

// ColumnInfo describes one column of a table.
type ColumnInfo struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Mode        string `json:"mode,omitempty"`
	Description string `json:"description,omitempty"`
}

// BucketProfile summarizes a GCS bucket's contents.
type BucketProfile struct {
	Bucket      string        `json:"bucket"`
	NumObjects  int           `json:"numObjects"`
	TotalBytes  int64         `json:"totalBytes"`
	Objects     []ObjectInfo  `json:"objects"`
	Previews    []FilePreview `json:"previews,omitempty"`
	Truncated   bool          `json:"truncated"`
}

// ObjectInfo describes one GCS object.
type ObjectInfo struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ContentType string `json:"contentType,omitempty"`
}

// FilePreview holds a short text preview of a data file (CSV/JSON/TSV/txt).
type FilePreview struct {
	Name    string `json:"name"`
	Preview string `json:"preview"`
}

const (
	maxObjectsListed   = 200
	maxPreviews        = 3
	previewMaxBytes    = 4096
	sampleRowsPerTable = 5
	maxTables          = 50
)

// buildDataProfile inspects every BQ dataset and GCS bucket resource of the DC
// and returns a compact profile. Errors on individual assets are collected
// rather than aborting the whole profile.
func buildDataProfile(ctx context.Context, resources []Resource, prog progressFunc) *DataProfile {
	profile := &DataProfile{}
	for _, r := range resources {
		switch r.ResourceType {
		case "BIG_QUERY_DATASET":
			prog(Progress{Phase: "bigquery", Message: fmt.Sprintf("Profiling BigQuery dataset %s.%s…", r.ProjectID, r.DatasetID)})
			tables, err := profileDataset(ctx, r.ProjectID, r.DatasetID, prog)
			if err != nil {
				profile.Errors = append(profile.Errors,
					fmt.Sprintf("BigQuery dataset %s.%s: %v", r.ProjectID, r.DatasetID, err))
				continue
			}
			profile.Tables = append(profile.Tables, tables...)
		case "GCS_BUCKET":
			prog(Progress{Phase: "gcs", Message: fmt.Sprintf("Scanning bucket gs://%s…", r.BucketName)})
			bp, err := profileBucket(ctx, r.BucketName)
			if err != nil {
				profile.Errors = append(profile.Errors,
					fmt.Sprintf("GCS bucket %s: %v", r.BucketName, err))
				continue
			}
			prog(Progress{Phase: "gcs", Message: fmt.Sprintf("gs://%s — %d objects, %s", bp.Bucket, bp.NumObjects, humanBytes(bp.TotalBytes))})
			profile.Buckets = append(profile.Buckets, *bp)
		}
	}
	return profile
}

// profileDataset lists tables in a BQ dataset and profiles each (schema +
// sample rows + row count).
func profileDataset(ctx context.Context, project, dataset string, prog progressFunc) ([]TableProfile, error) {
	client, err := bigquery.NewClient(ctx, project)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	ds := client.Dataset(dataset)
	it := ds.Tables(ctx)
	var profiles []TableProfile
	for len(profiles) < maxTables {
		t, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return profiles, err
		}
		tp, err := profileTable(ctx, client, project, dataset, t.TableID)
		if err != nil {
			// Record the table with just its name; skip detailed profiling.
			profiles = append(profiles, TableProfile{
				FQN:     fmt.Sprintf("%s.%s.%s", project, dataset, t.TableID),
				Project: project, Dataset: dataset, Table: t.TableID,
				Description: fmt.Sprintf("(profiling error: %v)", err),
			})
			continue
		}
		prog(Progress{Phase: "bigquery", Message: fmt.Sprintf("  %s — %s rows, %d columns", tp.Table, humanCount(tp.NumRows), len(tp.Columns))})
		profiles = append(profiles, *tp)
	}
	return profiles, nil
}

// profileTable reads a table's metadata (schema, row count, description) and a
// few sample rows.
func profileTable(ctx context.Context, client *bigquery.Client, project, dataset, table string) (*TableProfile, error) {
	md, err := client.DatasetInProject(project, dataset).Table(table).Metadata(ctx)
	if err != nil {
		return nil, err
	}
	tp := &TableProfile{
		FQN:         fmt.Sprintf("%s.%s.%s", project, dataset, table),
		Project:     project,
		Dataset:     dataset,
		Table:       table,
		Description: md.Description,
		NumRows:     md.NumRows,
		SizeBytes:   md.NumBytes,
	}
	for _, f := range md.Schema {
		mode := ""
		if f.Repeated {
			mode = "REPEATED"
		} else if f.Required {
			mode = "REQUIRED"
		} else {
			mode = "NULLABLE"
		}
		tp.Columns = append(tp.Columns, ColumnInfo{
			Name:        f.Name,
			Type:        string(f.Type),
			Mode:        mode,
			Description: f.Description,
		})
	}
	tp.SampleRows = sampleTableRows(ctx, client, project, dataset, table)
	return tp, nil
}

// sampleTableRows uses the BigQuery storage-free tabledata.list (via Read) to
// grab a handful of rows without running a billed query.
func sampleTableRows(ctx context.Context, client *bigquery.Client, project, dataset, table string) []map[string]any {
	sampleCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	it := client.DatasetInProject(project, dataset).Table(table).Read(sampleCtx)
	var rows []map[string]any
	for len(rows) < sampleRowsPerTable {
		var row map[string]bigquery.Value
		err := it.Next(&row)
		if err == iterator.Done || err != nil {
			break
		}
		conv := make(map[string]any, len(row))
		for k, v := range row {
			conv[k] = stringifyValue(v)
		}
		rows = append(rows, conv)
	}
	return rows
}

// stringifyValue makes BQ values JSON/LLM friendly (times → RFC3339, bytes →
// length marker, nested structs preserved as-is).
func stringifyValue(v bigquery.Value) any {
	switch t := v.(type) {
	case time.Time:
		return t.Format(time.RFC3339)
	case []byte:
		return fmt.Sprintf("<%d bytes>", len(t))
	default:
		return v
	}
}

// profileBucket lists objects in a GCS bucket and previews a few data files.
func profileBucket(ctx context.Context, bucket string) (*BucketProfile, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	bp := &BucketProfile{Bucket: bucket}
	it := client.Bucket(bucket).Objects(ctx, nil)
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return bp, err
		}
		bp.NumObjects++
		bp.TotalBytes += attrs.Size
		if len(bp.Objects) < maxObjectsListed {
			bp.Objects = append(bp.Objects, ObjectInfo{
				Name:        attrs.Name,
				Size:        attrs.Size,
				ContentType: attrs.ContentType,
			})
		} else {
			bp.Truncated = true
		}
	}

	// Preview a few likely-tabular text files, smallest first.
	previewable := make([]ObjectInfo, 0, len(bp.Objects))
	for _, o := range bp.Objects {
		if isPreviewable(o.Name) && o.Size > 0 {
			previewable = append(previewable, o)
		}
	}
	sort.Slice(previewable, func(i, j int) bool { return previewable[i].Size < previewable[j].Size })
	for i := 0; i < len(previewable) && len(bp.Previews) < maxPreviews; i++ {
		text, err := previewObject(ctx, client, bucket, previewable[i].Name)
		if err != nil {
			continue
		}
		bp.Previews = append(bp.Previews, FilePreview{Name: previewable[i].Name, Preview: text})
	}
	return bp, nil
}

// humanBytes formats a byte count as a short human-readable string.
func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	val := float64(n) / 1024
	i := 0
	for val >= 1024 && i < len(units)-1 {
		val /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", val, units[i])
}

// humanCount formats a large count with thousands separators.
func humanCount(n uint64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

func isPreviewable(name string) bool {
	lower := strings.ToLower(name)
	for _, ext := range []string{".csv", ".tsv", ".txt", ".json", ".jsonl", ".ndjson", ".md"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// previewObject reads up to previewMaxBytes from the start of an object.
func previewObject(ctx context.Context, client *storage.Client, bucket, name string) (string, error) {
	readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	r, err := client.Bucket(bucket).Object(name).NewRangeReader(readCtx, 0, previewMaxBytes)
	if err != nil {
		return "", err
	}
	defer r.Close()
	buf := make([]byte, previewMaxBytes)
	n, _ := r.Read(buf)
	return string(buf[:n]), nil
}
