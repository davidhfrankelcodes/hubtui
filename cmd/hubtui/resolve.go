package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

const resolveSynopsis = `Usage: hubtui resolve <image[:tag]> [--latest [--major]] [--arch <arch>] [--json]

Prints the tag's reference pinned to its multi-arch digest, for Dockerfiles
and scripts. A missing tag means latest. With --latest, prints the newest tag
of the same variant and version precision instead: postgres:17.4-alpine
resolves to the newest 17.x-alpine.

`

func (a *app) runResolve(ctx context.Context, args []string) int {
	fs := a.flagSet("resolve", resolveSynopsis)
	latest := fs.Bool("latest", false, "the newest tag in the same version line instead of the tag itself")
	major := fs.Bool("major", false, "with --latest, allow a newer major version (17-alpine to 18-alpine)")
	arch := fs.String("arch", "", "only tags with this platform: arm64, linux/arm64, arm/v7, ...")
	asJSON := fs.Bool("json", false, "print the tag as JSON, in the shape of hubtui tags --json")
	pos, code, ok := a.parse(fs, args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return a.usageError(fs, "resolve: expected exactly one image")
	}
	if *major && !*latest {
		return a.usageError(fs, "resolve: --major only applies with --latest")
	}
	ref, err := hub.ParseReference(pos[0])
	if err != nil {
		return a.usageError(fs, "resolve: "+err.Error())
	}
	if ref.Tag == "" {
		// A bare digest says which image, not which tag to look up; guessing
		// latest would silently re-pin it to something else.
		if ref.Digest != "" {
			return a.usageError(fs, "resolve: give a tag along with the digest, such as "+ref.Repo.String()+":latest@"+ref.Digest[:15]+"...")
		}
		ref.Tag = "latest"
	}
	platform := strings.ToLower(strings.TrimSpace(*arch))

	tag, err := a.resolveTag(ctx, ref, platform, *latest, *major)
	if err != nil {
		return a.fail(err)
	}
	pinned, err := hub.PinnedReference(ref.Repo, tag)
	if err != nil {
		return a.fail(err)
	}
	// Re-resolving a pinned reference is how people check a Dockerfile, so
	// say when the tag has moved; the new pin is still the answer.
	if ref.Digest != "" && tag.Name == ref.Tag && tag.Digest != ref.Digest {
		_, _ = fmt.Fprintf(a.stderr, "hubtui: note: %s:%s has moved from %s\n", ref.Repo, ref.Tag, ref.Digest)
	}

	if *asJSON {
		tj, err := newTagJSON(ref.Repo, tag)
		if err != nil {
			return a.fail(err)
		}
		return a.writeJSON(tj)
	}
	if _, err := fmt.Fprintln(a.stdout, pinned); err != nil {
		return a.fail(fmt.Errorf("writing output: %w", err))
	}
	return exitOK
}

func (a *app) resolveTag(ctx context.Context, ref hub.ImageRef, platform string, latest, crossMajor bool) (hub.Tag, error) {
	if latest {
		res, err := hub.LatestInLine(ctx, a.registry, ref.Repo, ref.Tag, hub.LineOptions{CrossMajor: crossMajor, Platform: platform})
		if errors.Is(err, hub.ErrNoVersion) {
			return hub.Tag{}, fmt.Errorf("%w; --latest needs a versioned tag such as %s:17-alpine", err, ref.Repo)
		}
		if err != nil {
			return hub.Tag{}, err
		}
		if res.Partial {
			_, _ = fmt.Fprintf(a.stderr, "hubtui: warning: docker hub stopped paging before every tag was seen; a newer one may exist (signing in lifts the limit)\n")
		}
		return res.Tag, nil
	}

	t, err := a.registry.Tag(ctx, ref.Repo, ref.Tag)
	if err != nil {
		return hub.Tag{}, err
	}
	if platform != "" && !t.HasPlatform(platform) {
		return hub.Tag{}, fmt.Errorf("%s:%s has no %s image", ref.Repo, ref.Tag, platform)
	}
	return *t, nil
}
