package hub

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrNoVersion means a tag has no version to compare, such as "latest".
var ErrNoVersion = errors.New("tag has no version")

// LineOptions widens or narrows the version line LatestInLine searches.
type LineOptions struct {
	// CrossMajor allows a newer major version: 17-alpine to 18-alpine.
	CrossMajor bool
	// Platform keeps only tags that have it, as Platform.Matches reads it.
	Platform string
}

// LineResult is the newest tag in a version line.
type LineResult struct {
	Tag Tag
	// Partial means Docker Hub stopped paging before every candidate was
	// seen (the anonymous cap), so a newer tag may exist.
	Partial bool
}

// LatestInLine finds the newest tag that could replace from: same variant,
// same number of version parts, and the same major version unless
// opts.CrossMajor (see TagName.SameLine). from itself counts, so the answer
// is from when nothing newer exists.
func LatestInLine(ctx context.Context, reg Registry, repo Repo, from string, opts LineOptions) (LineResult, error) {
	base := ParseTagName(from)
	if !base.HasVersion() {
		return LineResult{}, fmt.Errorf("%s:%s: %w to compare", repo, from, ErrNoVersion)
	}

	// A one-part release such as 17-alpine is the only member of its line
	// within its major, so one lookup answers it.
	if len(base.Version) == 1 && base.Pre == "" && !opts.CrossMajor {
		t, err := reg.Tag(ctx, repo, from)
		if err != nil {
			return LineResult{}, err
		}
		if opts.Platform != "" && !t.HasPlatform(opts.Platform) {
			return LineResult{}, fmt.Errorf("%s:%s has no %s image: %w", repo, from, opts.Platform, ErrNotFound)
		}
		return LineResult{Tag: *t}, nil
	}

	var (
		best    Tag
		bestTN  TagName
		found   bool
		partial bool
	)
	filter := lineFilter(base, opts.CrossMajor)
	for page := 1; ; page++ {
		p, err := reg.Tags(ctx, repo, TagsOptions{PageOptions: PageOptions{Page: page, PageSize: MaxPageSize}, Name: filter})
		if errors.Is(err, ErrPageLimit) {
			partial = true
			break
		}
		if err != nil {
			return LineResult{}, fmt.Errorf("searching the %s line of %s: %w", from, repo, err)
		}
		for _, t := range p.Tags {
			tn := ParseTagName(t.Name)
			if !base.SameLine(tn, opts.CrossMajor) || (opts.Platform != "" && !t.HasPlatform(opts.Platform)) {
				continue
			}
			// Ties keep the first seen, which is the most recently pushed.
			if !found || tn.Compare(bestTN) > 0 {
				best, bestTN, found = t, tn, true
			}
		}
		if !p.HasNext || len(p.Tags) == 0 {
			break
		}
	}
	if !found {
		what := "no tag"
		if opts.Platform != "" {
			what = "no " + opts.Platform + " tag"
		}
		return LineResult{}, fmt.Errorf("%s in the line of %s:%s: %w", what, repo, from, ErrNotFound)
	}
	return LineResult{Tag: best, Partial: partial}, nil
}

// lineFilter picks a substring every candidate's name must contain, so Hub
// filters server-side: one request instead of every page, and it reaches
// lines older than the anonymous paging cap. Within a major that is "17."
// (or "18" for one-part prereleases such as 18beta3, whose line is 18rc1 and
// 18); across majors the variant is the only shared text. Empty means no
// filter.
func lineFilter(base TagName, crossMajor bool) string {
	if !crossMajor {
		major := strconv.Itoa(base.Version[0])
		if len(base.Version) > 1 {
			major += "."
		}
		return major
	}
	// A variant rebuilt around a removed prerelease marker ("foo-rc-bar"
	// gives "foo-bar") is not in candidates' names verbatim.
	if base.Variant != "" && strings.Contains(base.Name, base.Variant) {
		return base.Variant
	}
	return ""
}
