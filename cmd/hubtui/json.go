package main

import (
	"context"
	"encoding/json"
	"errors"
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

func (a *app) searchJSON(ctx context.Context, query string) int {
	page, err := a.registry.Search(ctx, query, hub.PageOptions{})
	if err != nil {
		return a.fail(err)
	}
	out := make([]searchResultJSON, 0, len(page.Results))
	for _, r := range page.Results {
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

func (a *app) tagsJSON(ctx context.Context, repo hub.Repo, arch string, limit int) int {
	tags, err := collectTags(ctx, a.registry, repo, arch, limit)
	switch {
	case errors.Is(err, hub.ErrPageLimit):
		// Partial results are still useful; say why the list stops short.
		_, _ = fmt.Fprintf(a.stderr, "hubtui: warning: stopped after %d tags: %v\n", len(tags), err)
	case err != nil:
		return a.fail(err)
	}

	out := make([]tagJSON, 0, len(tags))
	for _, t := range tags {
		tj := tagJSON{Name: t.Name, MediaType: t.MediaType, Platforms: make([]platformJSON, 0, len(t.Platforms))}
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
		out = append(out, tj)
	}
	return a.writeJSON(out)
}

// collectTags pages through tags newest first until it has limit matches
// (0 means no limit) or runs out. On error it returns what it collected so far.
func collectTags(ctx context.Context, reg hub.Registry, repo hub.Repo, arch string, limit int) ([]hub.Tag, error) {
	// Without a filter every tag counts, so don't download more than needed.
	size := hub.MaxPageSize
	if arch == "" && limit > 0 {
		size = min(limit, hub.MaxPageSize)
	}

	var out []hub.Tag
	for page := 1; ; page++ {
		p, err := reg.Tags(ctx, repo, hub.TagsOptions{PageOptions: hub.PageOptions{Page: page, PageSize: size}})
		if err != nil {
			return out, fmt.Errorf("fetching page %d: %w", page, err)
		}
		for _, t := range p.Tags {
			if arch != "" && !t.HasPlatform(arch) {
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
