package hub

import (
	"strings"
	"time"
)

// SearchResult is one repository from a search.
type SearchResult struct {
	Repo        Repo
	Description string
	Stars       int
	Pulls       int64
	Official    bool
}

// SearchPage is one page of search results.
type SearchPage struct {
	Results []SearchResult
	Total   int
	Page    int
	HasNext bool
}

// Tag is one tag of a repository.
type Tag struct {
	Name string
	// Digest is the digest the tag points at: the multi-arch index for
	// multi-platform tags, the single manifest otherwise. It is empty for some
	// legacy schema-v1 tags.
	Digest    string
	MediaType string
	Pushed    time.Time
	// Platforms excludes attestation manifests.
	Platforms []Platform
}

// Platform is one per-platform image under a tag.
type Platform struct {
	OS      string
	Arch    string
	Variant string
	Digest  string
	Size    int64 // compressed
}

// String formats the platform as os/arch[/variant], omitting an unknown OS
// (legacy tags report it as empty).
func (p Platform) String() string {
	var parts []string
	for _, s := range []string{p.OS, p.Arch, p.Variant} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "/")
}

// TagPage is one page of tags.
type TagPage struct {
	Tags    []Tag
	Total   int
	Page    int
	HasNext bool
}

// Wire types mirror the JSON exactly; see testdata/ for recorded examples.

type searchResponse struct {
	Count   int    `json:"count"`
	Next    string `json:"next"`
	Results []struct {
		RepoName         string `json:"repo_name"`
		ShortDescription string `json:"short_description"`
		StarCount        int    `json:"star_count"`
		PullCount        int64  `json:"pull_count"`
		IsOfficial       bool   `json:"is_official"`
	} `json:"results"`
}

type tagsResponse struct {
	Count   int    `json:"count"`
	Next    string `json:"next"` // null on the last page
	Results []struct {
		Name          string    `json:"name"`
		Digest        string    `json:"digest"` // null on some legacy tags
		MediaType     string    `json:"media_type"`
		LastUpdated   time.Time `json:"last_updated"`
		TagLastPushed time.Time `json:"tag_last_pushed"`
		Images        []struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
			Variant      string `json:"variant"` // null when absent
			Digest       string `json:"digest"`
			Size         int64  `json:"size"`
		} `json:"images"`
	} `json:"results"`
}

// Matches reports whether p fits spec, which may be "arch", "os/arch",
// "arch/variant" or "os/arch/variant" (e.g. "arm64", "linux/arm64", "arm/v7").
func (p Platform) Matches(spec string) bool {
	switch spec {
	case "":
		return false
	case p.Arch, p.OS + "/" + p.Arch:
		return true
	}
	return p.Variant != "" && (spec == p.Arch+"/"+p.Variant || spec == p.OS+"/"+p.Arch+"/"+p.Variant)
}

// HasPlatform reports whether any of t's platforms matches spec.
func (t Tag) HasPlatform(spec string) bool {
	for _, p := range t.Platforms {
		if p.Matches(spec) {
			return true
		}
	}
	return false
}
