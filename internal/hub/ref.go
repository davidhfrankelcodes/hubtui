package hub

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// These follow the distribution reference grammar. Hub data is trusted, but a
// malformed reference on someone's clipboard is the worst failure this tool
// can have, so every reference is validated before it is handed out.
var (
	tagRE    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	digestRE = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

// ErrNoDigest means a tag cannot be pinned by digest.
var ErrNoDigest = errors.New("no pullable digest")

// Reference returns "image:tag" in the short form docker accepts, e.g.
// "nginx:1.27" or "grafana/grafana:11.0.0".
func Reference(repo Repo, tag string) (string, error) {
	if !componentRE.MatchString(repo.Namespace) || !componentRE.MatchString(repo.Name) {
		return "", fmt.Errorf("invalid repository %q", repo.Namespace+"/"+repo.Name)
	}
	if !tagRE.MatchString(tag) {
		return "", fmt.Errorf("invalid tag %q", tag)
	}
	return repo.String() + ":" + tag, nil
}

// PinnedReference returns "image:tag@sha256:..." using the tag's own digest,
// which for multi-platform tags is the index, so the reference works on every
// architecture the tag supports.
func PinnedReference(repo Repo, t Tag) (string, error) {
	ref, err := Reference(repo, t.Name)
	if err != nil {
		return "", err
	}
	if isSchema1(t.MediaType) {
		// Current Docker refuses schema-1 manifests, so a pinned reference
		// would only fail later and less clearly.
		return "", fmt.Errorf("%s uses a legacy schema-1 manifest: %w", ref, ErrNoDigest)
	}
	if t.Digest == "" {
		return "", fmt.Errorf("docker hub reports no digest for %s: %w", ref, ErrNoDigest)
	}
	if !digestRE.MatchString(t.Digest) {
		return "", fmt.Errorf("unexpected digest %q for %s", t.Digest, ref)
	}
	return ref + "@" + t.Digest, nil
}

func isSchema1(mediaType string) bool {
	switch mediaType {
	case "application/vnd.docker.distribution.manifest.v1+prettyjws",
		"application/vnd.docker.distribution.manifest.v1+json":
		return true
	}
	return false
}

// ImageRef is a parsed image reference such as "postgres:17-alpine" or
// "docker.io/grafana/grafana:11.0.0@sha256:...".
type ImageRef struct {
	Repo Repo
	// Tag is empty when the reference has none; docker reads that as
	// "latest" unless a digest is given.
	Tag string
	// Digest is empty when the reference is not pinned.
	Digest string
}

// ParseReference splits a Docker Hub image reference into repository, tag
// and digest, validating each the way docker would.
func ParseReference(s string) (ImageRef, error) {
	s = strings.TrimSpace(s)
	var ref ImageRef
	name, digest, pinned := strings.Cut(s, "@")
	if pinned {
		if !digestRE.MatchString(digest) {
			return ImageRef{}, fmt.Errorf("%q: invalid digest %q", s, digest)
		}
		ref.Digest = digest
	}
	// The tag follows the last path segment's colon; any earlier colon
	// belongs to a registry host, which ParseRepo rejects.
	if i := strings.LastIndexByte(name, ':'); i > strings.LastIndexByte(name, '/') {
		name, ref.Tag = name[:i], name[i+1:]
		if !tagRE.MatchString(ref.Tag) {
			return ImageRef{}, fmt.Errorf("%q: invalid tag %q", s, ref.Tag)
		}
	}
	repo, err := ParseRepo(name)
	if err != nil {
		return ImageRef{}, err
	}
	ref.Repo = repo
	return ref, nil
}
