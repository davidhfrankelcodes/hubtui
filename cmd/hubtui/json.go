package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

// The JSON shapes below are a public interface for scripts: add fields freely,
// but never rename or remove one.

type searchResultJSON struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	Repository  string `json:"repository"`
	Official    bool   `json:"official"`
	Stars       int    `json:"stars"`
	Pulls       int64  `json:"pulls"`
	Description string `json:"description"`
}

type tagJSON struct {
	Name string `json:"name"`
	// Reference and PinnedReference come from the same code as the TUI's yank
	// keys. PinnedReference is null when the tag cannot be pinned.
	Reference       string  `json:"reference"`
	PinnedReference *string `json:"pinned_reference"`
	// Digest is null, not "", when Hub has none, so `select(.digest)` works in jq.
	Digest    *string        `json:"digest"`
	MediaType string         `json:"media_type"`
	Pushed    *time.Time     `json:"pushed"`
	Platforms []platformJSON `json:"platforms"`
}

type platformJSON struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant,omitempty"`
	Digest       string `json:"digest"`
	Size         int64  `json:"size"`
}

func (a *app) searchJSON(results []hub.SearchResult) int {
	out := make([]searchResultJSON, 0, len(results))
	for _, r := range results {
		out = append(out, searchResultJSON{
			Name:        r.Repo.String(),
			Namespace:   r.Repo.Namespace,
			Repository:  r.Repo.Name,
			Official:    r.Official,
			Stars:       r.Stars,
			Pulls:       r.Pulls,
			Description: r.Description,
		})
	}
	return a.writeJSON(out)
}

// tagFilter selects which tags `hubtui tags` prints.
type tagFilter struct {
	arch   string
	stable bool
}

func (f tagFilter) empty() bool { return f.arch == "" && !f.stable }

func (f tagFilter) keep(t hub.Tag) bool {
	return (f.arch == "" || t.HasPlatform(f.arch)) && (!f.stable || !hub.IsPrerelease(t.Name))
}

func (a *app) tagsJSON(repo hub.Repo, tags []hub.Tag) int {
	out := make([]tagJSON, 0, len(tags))
	for _, t := range tags {
		tj, err := newTagJSON(repo, t)
		if err != nil {
			// Never print a list with a bad reference in it for scripts to use.
			return a.fail(err)
		}
		out = append(out, tj)
	}
	return a.writeJSON(out)
}

// newTagJSON is shared by tags and resolve, so a tag has one JSON shape.
func newTagJSON(repo hub.Repo, t hub.Tag) (tagJSON, error) {
	ref, err := hub.Reference(repo, t.Name)
	if err != nil {
		return tagJSON{}, err
	}
	tj := tagJSON{Name: t.Name, Reference: ref, MediaType: t.MediaType, Platforms: make([]platformJSON, 0, len(t.Platforms))}
	if pinned, err := hub.PinnedReference(repo, t); err == nil {
		tj.PinnedReference = &pinned
	}
	if t.Digest != "" {
		tj.Digest = &t.Digest
	}
	if !t.Pushed.IsZero() {
		tj.Pushed = &t.Pushed
	}
	for _, p := range t.Platforms {
		tj.Platforms = append(tj.Platforms, platformJSON{
			OS: p.OS, Architecture: p.Arch, Variant: p.Variant, Digest: p.Digest, Size: p.Size,
		})
	}
	return tj, nil
}

// collectTags pages through tags newest first until it has limit matches
// (0 means no limit) or runs out. On error it returns what it collected so far.
func collectTags(ctx context.Context, reg hub.Registry, repo hub.Repo, filter tagFilter, limit int) ([]hub.Tag, error) {
	// Without a filter every tag counts, so don't download more than needed.
	size := hub.MaxPageSize
	if filter.empty() && limit > 0 {
		size = min(limit, hub.MaxPageSize)
	}

	var out []hub.Tag
	for page := 1; ; page++ {
		p, err := reg.Tags(ctx, repo, hub.TagsOptions{PageOptions: hub.PageOptions{Page: page, PageSize: size}})
		if err != nil {
			return out, fmt.Errorf("fetching page %d: %w", page, err)
		}
		for _, t := range p.Tags {
			if !filter.keep(t) {
				continue
			}
			out = append(out, t)
			if limit > 0 && len(out) == limit {
				return out, nil
			}
		}
		if !p.HasNext || len(p.Tags) == 0 {
			return out, nil
		}
	}
}

func (a *app) writeJSON(v any) int {
	enc := json.NewEncoder(a.stdout)
	enc.SetEscapeHTML(false) // descriptions contain <, > and & that should stay readable
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return a.fail(fmt.Errorf("writing output: %w", err))
	}
	return exitOK
}

func (a *app) fail(err error) int {
	_, _ = fmt.Fprintf(a.stderr, "hubtui: %v\n", err)
	return exitError
}
