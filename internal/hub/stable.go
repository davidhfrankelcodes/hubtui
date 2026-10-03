package hub

import (
	"strings"
	"unicode"
)

// isPrereleaseWord reports whether a tag-name word marks a build as not a
// release. The words come from the tags of popular images: rc/alpha/beta
// (python, terraform), nightly/dev/preview (grafana), tip (golang), and
// Debian's suites.
func isPrereleaseWord(w string) bool {
	switch w {
	case "alpha", "beta", "rc", "pre", "preview",
		"dev", "devel", "develop", "nightly", "tip",
		"edge", "canary", "snapshot", "insiders",
		"main", "master", "sha",
		"unstable", "sid", "testing", "experimental":
		return true
	}
	return false
}

// isPrereleaseLetter covers single letters that mark a prerelease only when
// a number follows, as in python's 3.15.0b4 or redis's 8.8-m03; on their
// own they are too short to mean anything.
func isPrereleaseLetter(w string) bool {
	return w == "a" || w == "b" || w == "m"
}

// IsPrerelease reports whether a tag name looks like a prerelease, a dev
// build or a commit build. It is a heuristic on the name alone: it matches
// whole words, so "mainline" and "alpine" are not "main" or "a". Date-stamped
// tags are not prereleases; Debian and Ubuntu use them for stable snapshots.
func IsPrerelease(name string) bool {
	name = strings.ToLower(name)
	if looksLikeCommit(name) {
		return true
	}
	runes := []rune(name)
	for i := 0; i < len(runes); {
		if !unicode.IsLetter(runes[i]) {
			i++
			continue
		}
		j := i
		for j < len(runes) && unicode.IsLetter(runes[j]) {
			j++
		}
		word := string(runes[i:j])
		if isPrereleaseWord(word) || (isPrereleaseLetter(word) && j < len(runes) && unicode.IsDigit(runes[j])) {
			return true
		}
		i = j
	}
	return false
}

// looksLikeCommit matches a bare abbreviated or full git hash. Requiring a
// letter keeps all-digit tags such as dates and build numbers out.
func looksLikeCommit(name string) bool {
	if len(name) < 7 || len(name) > 40 {
		return false
	}
	letter := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'f':
			letter = true
		case r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return letter
}
