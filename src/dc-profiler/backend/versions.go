package main

import (
	"sort"
	"strings"
	"time"
)

// noVersionID identifies the synthetic bucket of resources that live at the
// workspace root, outside any version folder.
const noVersionID = "__none__"

// defaultVersionProp is the workspace property holding the chosen default
// version's folder id.
const defaultVersionProp = "terra-default-version-id"

// publishedDateProp on a version folder marks it published (and when).
const publishedDateProp = "terra-published-date"

// Version is a DC version (a top-level WSM folder) plus derived state the UI
// needs to populate the version picker.
type Version struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	IsPublished   bool   `json:"isPublished"`
	PublishedDate string `json:"publishedDate,omitempty"`
	IsDefault     bool   `json:"isDefault"`
	ResourceCount int    `json:"resourceCount"`
}

// annotateResourceVersions sets each resource's VersionID to the top-level
// folder (version) it belongs to, walking the folder parent chain, and its
// FolderPath to the human-readable folder chain (e.g. "Version 1/raw/2023").
// Resources at the workspace root (or referencing an unknown folder) get
// noVersionID and an empty FolderPath.
func annotateResourceVersions(folders []Folder, resources []Resource) {
	parent := make(map[string]string, len(folders))
	name := make(map[string]string, len(folders))
	exists := make(map[string]bool, len(folders))
	for _, f := range folders {
		parent[f.ID] = f.ParentFolderID
		name[f.ID] = f.DisplayName
		exists[f.ID] = true
	}
	rootOf := func(fid string) string {
		if fid == "" || !exists[fid] {
			return noVersionID
		}
		seen := map[string]bool{}
		for {
			p := parent[fid]
			if p == "" {
				return fid // top-level folder == version
			}
			if seen[fid] { // cycle guard
				return fid
			}
			seen[fid] = true
			fid = p
		}
	}
	pathOf := func(fid string) string {
		if fid == "" || !exists[fid] {
			return ""
		}
		var parts []string
		seen := map[string]bool{}
		for fid != "" && exists[fid] && !seen[fid] {
			seen[fid] = true
			parts = append([]string{name[fid]}, parts...)
			fid = parent[fid]
		}
		return strings.Join(parts, "/")
	}
	for i := range resources {
		resources[i].VersionID = rootOf(resources[i].FolderID)
		resources[i].FolderPath = pathOf(resources[i].FolderID)
	}
}

// buildVersions annotates the resources and returns the DC's versions (plus a
// synthetic "No version" entry when root-level resources exist) along with the
// recommended default selection: default version → latest published → first →
// no-version.
func buildVersions(dc *DataCollection, folders []Folder, resources []Resource) ([]Version, string) {
	annotateResourceVersions(folders, resources)

	counts := map[string]int{}
	for _, r := range resources {
		counts[r.VersionID]++
	}
	defaultID := dc.getProperty(defaultVersionProp)

	var versions []Version
	for _, f := range folders {
		if f.ParentFolderID != "" {
			continue // only top-level folders are versions
		}
		pub := folderProp(f, publishedDateProp)
		versions = append(versions, Version{
			ID:            f.ID,
			Name:          f.DisplayName,
			IsPublished:   pub != "",
			PublishedDate: pub,
			IsDefault:     f.ID == defaultID,
			ResourceCount: counts[f.ID],
		})
	}

	// Sort: default first, then published newest-first, then drafts.
	sort.SliceStable(versions, func(i, j int) bool {
		a, b := versions[i], versions[j]
		if a.IsDefault != b.IsDefault {
			return a.IsDefault
		}
		if a.IsPublished != b.IsPublished {
			return a.IsPublished
		}
		return parsePublished(a.PublishedDate).After(parsePublished(b.PublishedDate))
	})

	recommended := ""
	for _, v := range versions {
		if v.IsDefault {
			recommended = v.ID
			break
		}
	}
	if recommended == "" {
		for _, v := range versions {
			if v.IsPublished {
				recommended = v.ID
				break
			}
		}
	}
	if recommended == "" && len(versions) > 0 {
		recommended = versions[0].ID
	}

	if counts[noVersionID] > 0 {
		versions = append(versions, Version{
			ID:            noVersionID,
			Name:          "No version",
			ResourceCount: counts[noVersionID],
		})
	}
	if recommended == "" {
		recommended = noVersionID
	}
	return versions, recommended
}

// filterResourcesByVersion returns the resources belonging to versionID. The
// resources must already be annotated (via buildVersions/annotateResourceVersions).
func filterResourcesByVersion(versionID string, resources []Resource) []Resource {
	var out []Resource
	for _, r := range resources {
		if r.VersionID == versionID {
			out = append(out, r)
		}
	}
	return out
}

func folderProp(f Folder, key string) string {
	for _, p := range f.Properties {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}

// parsePublished parses the folder's published-date property, tolerating the
// couple of formats WSM/clients emit. Returns the zero time if unparseable.
func parsePublished(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC1123, time.RFC1123Z, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
