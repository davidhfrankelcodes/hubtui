package hub

import (
	"context"
	"errors"
	"fmt"
)

// CheckStatus says whether a reference needs changing.
type CheckStatus string

// The statuses of CheckReference. All but CheckOK come with a suggestion.
const (
	// CheckOK means the reference is pinned to its tag's current image and
	// no newer tag is in its line.
	CheckOK CheckStatus = "ok"
	// CheckOutdated means a newer tag is in the reference's version line.
	CheckOutdated CheckStatus = "outdated"
	// CheckMoved means the tag now points at a different image than the
	// pinned digest.
	CheckMoved CheckStatus = "moved"
	// CheckUnpinned means the reference has no digest.
	CheckUnpinned CheckStatus = "unpinned"
	// CheckSingleArch means the digest is one platform's image rather than
	// the tag's multi-arch index, so it fails on other architectures.
	CheckSingleArch CheckStatus = "single-arch"
)

// CheckResult is what CheckReference found.
type CheckResult struct {
	Status CheckStatus
	// Suggested is the tag to pin to; zero for CheckOK.
	Suggested Tag
	// Partial means paging stopped early, so a newer tag may exist.
	Partial bool
}

// CheckReference reports whether ref is pinned, current and the newest in
// its version line. Versioned tags look for a newer tag the way
// LatestInLine does; tags without a version (latest, alpine) only have
// their pin checked.
func CheckReference(ctx context.Context, reg Registry, ref ImageRef, opts LineOptions) (CheckResult, error) {
	if ref.Tag == "" {
		return CheckResult{}, errors.New("a reference without a tag cannot be checked")
	}

	var res CheckResult
	var current *Tag
	if base := ParseTagName(ref.Tag); base.HasVersion() {
		line, err := LatestInLine(ctx, reg, ref.Repo, ref.Tag, opts)
		if err != nil {
			return CheckResult{}, err
		}
		res.Partial = line.Partial
		// Equal versions written differently (24.04 and 24.4) are not an
		// upgrade, and an older answer means ref itself was passed over
		// (wrong platform, unpinnable), which the lookup below reports.
		if ParseTagName(line.Tag.Name).Compare(base) > 0 {
			res.Status, res.Suggested = CheckOutdated, line.Tag
			return res, nil
		}
		if line.Tag.Name == ref.Tag {
			current = &line.Tag
		}
	}
	if current == nil {
		t, err := reg.Tag(ctx, ref.Repo, ref.Tag)
		if err != nil {
			return CheckResult{}, err
		}
		current = t
	}
	if opts.Platform != "" && !current.HasPlatform(opts.Platform) {
		return CheckResult{}, fmt.Errorf("%s:%s has no %s image", ref.Repo, ref.Tag, opts.Platform)
	}
	if _, err := PinnedReference(ref.Repo, *current); err != nil {
		return CheckResult{}, err
	}

	res.Suggested = *current
	switch {
	case ref.Digest == "":
		res.Status = CheckUnpinned
	case ref.Digest == current.Digest:
		res.Status, res.Suggested = CheckOK, Tag{}
	case current.HasImageDigest(ref.Digest):
		res.Status = CheckSingleArch
	default:
		res.Status = CheckMoved
	}
	return res, nil
}

// HasImageDigest reports whether digest is one of t's per-platform images.
func (t Tag) HasImageDigest(digest string) bool {
	for _, p := range t.Platforms {
		if p.Digest == digest {
			return true
		}
	}
	return false
}
