package hub

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrNoVersion means a tag has no version to compare, such as "latest".
var ErrNoVersion = errors.New("tag has no version")

// LineOptions widens or narrows the version line LatestInLine searches.
type LineOptions struct {
	// CrossMajor allows a newer major version: 17-alpine to 18-alpine.
	CrossMajor bool
	// SameMinor keeps the minor version too: python 3.12.1 moves to
	// 3.12.9, never 3.13.0. It wins over CrossMajor.
	SameMinor bool
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

// LatestInLine finds the newest pinnable tag that could replace from: same
// variant, same precision, and the same major version, or minor with
// opts.SameMinor, or any with opts.CrossMajor (see TagName.SameLine). from itself counts, so the answer
// is from when nothing newer exists.
func LatestInLine(ctx context.Context, reg Registry, repo Repo, from string, opts LineOptions) (LineResult, error) {
	base := ParseTagName(from)
	if !base.HasVersion() {
		return LineResult{}, fmt.Errorf("%s:%s: %w to compare", repo, from, ErrNoVersion)
	}

	// When every version part is held fixed (17-alpine within its major,
	// 3.12-slim within its minor) a release is its line's only member, so
	// one lookup answers it.
	fixed := opts.fixed()
	if fixed >= len(base.Version) && base.Build == 0 && base.Pre == "" {
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
		best       Tag
		bestTN     TagName
		found      bool
		partial    bool
		unpinnable bool
	)
	filter := lineFilter(base, fixed)
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
			if !base.SameLine(tn, fixed) || (opts.Platform != "" && !t.HasPlatform(opts.Platform)) {
				continue
			}
			// An unpinnable tag (legacy, no digest) is no answer for resolve,
			// so the newest pinnable one wins instead.
			if !pinnable(t) {
				unpinnable = true
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
	if !found && partial {
		return LineResult{}, fmt.Errorf("no tag in the line of %s:%s before paging stopped: %w", repo, from, ErrPageLimit)
	}
	if !found && unpinnable {
		return LineResult{}, fmt.Errorf("the line of %s:%s has only legacy tags: %w", repo, from, ErrNoDigest)
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

// fixed is how many leading version parts the line holds.
func (o LineOptions) fixed() int {
	switch {
	case o.SameMinor:
		return 2
	case o.CrossMajor:
		return 0
	}
	return 1
}

// lineFilter picks a substring every candidate's name must contain, so Hub
// filters server-side: one request instead of every page, and it reaches
// lines older than the anonymous paging cap. With parts held fixed that is
// their text, "17." or "3.12." ("18" alone for one-part prereleases such as
// 18beta3, whose line is 18rc1 and 18); with none, the variant is the only
// shared text. Empty means no filter.
func lineFilter(base TagName, fixed int) string {
	if n := min(fixed, len(base.Version)); n > 0 {
		prefix := writtenVersion(base.Name, n)
		if len(base.Version) > n {
			prefix += "."
		}
		return prefix
	}
	// A variant rebuilt around a removed prerelease marker ("foo-rc-bar"
	// gives "foo-bar") is not in candidates' names verbatim.
	if base.Variant != "" && strings.Contains(base.Name, base.Variant) {
		return base.Variant
	}
	return ""
}

// writtenVersion is the first n version parts of name as written, so
// ubuntu's "24.04" filters as "24.04", not "24.4". name must have parsed
// with at least n parts.
func writtenVersion(name string, n int) string {
	s := name
	if len(s) > 1 && (s[0] == 'v' || s[0] == 'V') && isASCIIDigit(s[1]) {
		s = s[1:]
	}
	end := 0
	for part := 0; part < n; part++ {
		if part > 0 {
			end++ // the dot
		}
		for end < len(s) && isASCIIDigit(s[end]) {
			end++
		}
	}
	return s[:end]
}

// pinnable reports whether PinnedReference can pin t.
func pinnable(t Tag) bool {
	return digestRE.MatchString(t.Digest) && !isSchema1(t.MediaType)
}
