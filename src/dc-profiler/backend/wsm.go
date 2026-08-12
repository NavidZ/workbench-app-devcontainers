package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var (
	wsmBaseURLMu sync.Mutex
	wsmBaseURL   string // cached once resolved successfully
)

// getWSMBaseURL resolves the Workspace Manager REST base URL from
// `wb status --format=json`, caching the first successful result. It is
// resolved lazily (not at process start) because the `wb` CLI is installed and
// authenticated by the devcontainer postCreate hook, which may race with the
// server coming up. The returned URL has no trailing slash and no /api suffix
// (endpoints below include the full /api/... path).
//
// There is deliberately no hard-coded fallback: if the CLI can't tell us the
// WSM URL, the environment is misconfigured and we fail loudly rather than
// silently talking to the wrong (or a stale default) backend.
func getWSMBaseURL() (string, error) {
	wsmBaseURLMu.Lock()
	defer wsmBaseURLMu.Unlock()
	if wsmBaseURL != "" {
		return wsmBaseURL, nil
	}
	out, err := exec.Command("wb", "status", "--format=json").Output()
	if err != nil {
		return "", fmt.Errorf("resolve WSM URL: wb status failed: %w", err)
	}
	var status struct {
		Server struct {
			WorkspaceManagerURI string `json:"workspaceManagerUri"`
		} `json:"server"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return "", fmt.Errorf("resolve WSM URL: parse wb status output: %w", err)
	}
	if status.Server.WorkspaceManagerURI == "" {
		return "", fmt.Errorf("resolve WSM URL: wb status did not report a workspaceManagerUri")
	}
	wsmBaseURL = strings.TrimRight(status.Server.WorkspaceManagerURI, "/")
	logf("WSM base URL: %s", wsmBaseURL)
	return wsmBaseURL, nil
}

// getToken returns a fresh access token via the wb CLI. We never read the
// on-disk credential files directly.
func getToken() (string, error) {
	out, err := exec.Command("wb", "auth", "print-access-token").Output()
	if err != nil {
		return "", fmt.Errorf("wb auth print-access-token: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// wsmRequest performs an authenticated JSON request against WSM and decodes the
// response body into out (may be nil to discard). body may be nil.
func wsmRequest(method, path string, body any, out any) error {
	token, err := getToken()
	if err != nil {
		return err
	}

	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	base, err := getWSMBaseURL()
	if err != nil {
		return err
	}
	url := base + path
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, string(respBody))
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode response from %s %s: %w", method, path, err)
		}
	}
	return nil
}

// Property is a single WSM workspace property key/value.
type Property struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// DataCollection is the trimmed view of a WSM workspace tagged as a DC.
type DataCollection struct {
	ID          string     `json:"id"`          // UUID
	UserFacingID string    `json:"userFacingId"`
	DisplayName string     `json:"displayName"`
	Description string     `json:"description"`
	HighestRole string     `json:"highestRole"`
	Properties  []Property `json:"properties"`
	GCPProject  string     `json:"gcpProject,omitempty"`
	WebURL      string     `json:"webUrl,omitempty"` // Workbench UI page for this DC
}

// wsmWorkspace mirrors the fields we consume from a WSM workspace description.
type wsmWorkspace struct {
	ID           string     `json:"id"`
	UserFacingID string     `json:"userFacingId"`
	DisplayName  string     `json:"displayName"`
	Description  string     `json:"description"`
	HighestRole  string     `json:"highestRole"`
	Properties   []Property `json:"properties"`
	GCPContext   *struct {
		ProjectID string `json:"projectId"`
	} `json:"gcpContext,omitempty"`
}

func (w wsmWorkspace) toDataCollection() DataCollection {
	dc := DataCollection{
		ID:           w.ID,
		UserFacingID: w.UserFacingID,
		DisplayName:  w.DisplayName,
		Description:  w.Description,
		HighestRole:  w.HighestRole,
		Properties:   w.Properties,
	}
	if w.GCPContext != nil {
		dc.GCPProject = w.GCPContext.ProjectID
	}
	dc.WebURL = workbenchDCURL(w.UserFacingID)
	return dc
}

// workbenchDCURL builds the Workbench web-app URL for a data collection's page.
// The host is derived at runtime from the resolved WSM URI (which comes from
// `wb status`), so this works across environments without any hardcoded host.
func workbenchDCURL(userFacingID string) string {
	if userFacingID == "" {
		return ""
	}
	base, err := getWSMBaseURL()
	if err != nil {
		return ""
	}
	u, err := neturl.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host + "/data-collections/" + userFacingID
}

// listDataCollections returns all data collections on which the caller has
// OWNER or WRITER access, paging through the filtered workspaces endpoint.
func listDataCollections() ([]DataCollection, error) {
	const pageSize = 100
	var result []DataCollection
	offset := 0
	for {
		reqBody := map[string]any{
			"limit":  pageSize,
			"offset": offset,
			"properties": []Property{
				{Key: "terra-type", Value: "data-collection"},
			},
		}
		var page struct {
			Workspaces []wsmWorkspace `json:"workspaces"`
		}
		if err := wsmRequest(http.MethodPost, "/api/workspaces/v2/filtered", reqBody, &page); err != nil {
			return nil, err
		}
		for _, w := range page.Workspaces {
			role := strings.ToUpper(w.HighestRole)
			if role == "OWNER" || role == "WRITER" {
				result = append(result, w.toDataCollection())
			}
		}
		if len(page.Workspaces) < pageSize {
			break
		}
		offset += pageSize
	}
	return result, nil
}

// getDataCollection fetches a single workspace by UUID.
func getDataCollection(id string) (*DataCollection, error) {
	var w wsmWorkspace
	if err := wsmRequest(http.MethodGet, "/api/workspaces/v1/"+id, nil, &w); err != nil {
		return nil, err
	}
	dc := w.toDataCollection()
	return &dc, nil
}

// Resource is a trimmed WSM resource description (bucket or BQ dataset).
type Resource struct {
	Name         string `json:"name"`
	ResourceType string `json:"resourceType"` // GCS_BUCKET, BIG_QUERY_DATASET, ...
	// GCS
	BucketName string `json:"bucketName,omitempty"`
	// BigQuery
	ProjectID string `json:"projectId,omitempty"`
	DatasetID string `json:"datasetId,omitempty"`
}

// listResources returns the controlled + referenced resources of a workspace,
// flattening the WSM resource metadata into a simpler shape.
func listResources(id string) ([]Resource, error) {
	var raw struct {
		Resources []struct {
			Metadata struct {
				Name         string `json:"name"`
				ResourceType string `json:"resourceType"`
			} `json:"metadata"`
			ResourceAttributes struct {
				GcpGcsBucket *struct {
					BucketName string `json:"bucketName"`
				} `json:"gcpGcsBucket,omitempty"`
				GcpBqDataset *struct {
					ProjectID string `json:"projectId"`
					DatasetID string `json:"datasetId"`
				} `json:"gcpBqDataset,omitempty"`
			} `json:"resourceAttributes"`
		} `json:"resources"`
	}
	path := fmt.Sprintf("/api/workspaces/v1/%s/resources?offset=0&limit=1000", id)
	if err := wsmRequest(http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	var out []Resource
	for _, r := range raw.Resources {
		res := Resource{Name: r.Metadata.Name, ResourceType: r.Metadata.ResourceType}
		if r.ResourceAttributes.GcpGcsBucket != nil {
			res.BucketName = r.ResourceAttributes.GcpGcsBucket.BucketName
		}
		if r.ResourceAttributes.GcpBqDataset != nil {
			res.ProjectID = r.ResourceAttributes.GcpBqDataset.ProjectID
			res.DatasetID = r.ResourceAttributes.GcpBqDataset.DatasetID
		}
		out = append(out, res)
	}
	return out, nil
}

// updateDescription sets the built-in workspace description via PATCH.
func updateDescription(id, description string) error {
	body := map[string]any{"description": description}
	return wsmRequest(http.MethodPatch, "/api/workspaces/v1/"+id, body, nil)
}

// setProperties upserts workspace properties. Values may contain commas and
// newlines; the REST API preserves them verbatim (unlike the CLI).
func setProperties(id string, props []Property) error {
	if len(props) == 0 {
		return nil
	}
	return wsmRequest(http.MethodPost, "/api/workspaces/v1/"+id+"/properties", props, nil)
}

// getProperty returns the value of a property, or "" if unset.
func (dc *DataCollection) getProperty(key string) string {
	for _, p := range dc.Properties {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}
