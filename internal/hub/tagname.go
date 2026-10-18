package hub

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// TagName is a tag name split into the parts that version-aware tools
// compare: "17.4-alpine3.21" is version 17.4 with variant "alpine3.21".
type TagName struct {
	Name string
	// Version is nil for tags that do not start with a version: latest,
	// alpine, codenames, date stamps and commit hashes.
	Version []int
	// Build is the number after an underscore, as Eclipse Temurin writes
	// builds (21.0.12.1_1); 0 when there is none.
	Build int
	// Pre holds the prerelease markers, lowercased and joined with "-":
	// "rc3" for 8.6.0RC3, "rc.1", "dev3", "b4". Empty for a release.
	Pre string
	// Variant is everything else after the version, as written:
	// "alpine3.24", "trixie-slim", "management-alpine", "ubi10".
	Variant string
}

// ParseTagName splits a tag name into version, build, prerelease and
// variant. It is deliberately strict: a name it cannot read with confidence
// gets no version rather than a guessed one, because callers use the
// version to pick a tag for someone's Dockerfile.
func ParseTagName(name string) TagName {
	none := TagName{Name: name}
	if looksLikeCommit(strings.ToLower(name)) {
		return none
	}

	s := name
	if len(s) > 1 && (s[0] == 'v' || s[0] == 'V') && isASCIIDigit(s[1]) {
		s = s[1:]
	}

	var version []int
	for {
		digits, rest := leadingASCIIDigits(s)
		// More than four digits up front is a date or a build number, and
		// more than nine cannot be a version part (and would overflow).
		if digits == "" || (version == nil && len(digits) > 4) || len(digits) > 9 {
			break
		}
		n, _ := strconv.Atoi(digits)
		version = append(version, n)
		s = rest
		if len(s) < 2 || s[0] != '.' || !isASCIIDigit(s[1]) {
			break
		}
		s = s[1:]
	}
	if version == nil {
		return none
	}

	t := TagName{Name: name, Version: version}
	var pre []string

	// An underscore introduces either a build number (21.0.12.1_1) or a
	// prerelease (3.17.0_rc1).
	if strings.HasPrefix(s, "_") {
		tok, rest := cutSegment(s[1:])
		switch {
		case isAllDigits(tok) && len(tok) <= 9:
			t.Build, _ = strconv.Atoi(tok)
		case isPrereleaseSegment(tok):
			pre = append(pre, strings.ToLower(tok))
		default:
			return none
		}
		s = rest
	}

	// Letters straight after the number must be a prerelease marker
	// (8.6.0RC3, 3.15.0b4, 18beta3); anything else, such as Java's 8u412,
	// is not a version this parser understands.
	if s != "" && s[0] != '-' {
		tok, rest := cutSegment(s)
		if !isPrereleaseSegment(tok) {
			return none
		}
		pre = append(pre, strings.ToLower(tok))
		s = rest
	}

	if s != "" {
		var variant []string
		for _, seg := range strings.Split(s[1:], "-") {
			// Underscores join words inside a segment too, as in
			// prometheus's v2.43.0-rc.1_stringlabels.
			var keep []string
			for _, part := range strings.Split(seg, "_") {
				if isPrereleaseSegment(part) {
					pre = append(pre, strings.ToLower(part))
					continue
				}
				keep = append(keep, part)
			}
			if keep != nil {
				variant = append(variant, strings.Join(keep, "_"))
			}
		}
		t.Variant = strings.Join(variant, "-")
	}
	t.Pre = strings.Join(pre, "-")
	return t
}

// HasVersion reports whether the name starts with a version.
func (t TagName) HasVersion() bool { return t.Version != nil }

// Compare orders tags by version, then build, then a release ahead of its
// prereleases (rc1 < release), then prereleases by stage and number. The
// variant does not take part; compare tags of the same variant.
func (t TagName) Compare(o TagName) int {
	if c := slices.Compare(t.Version, o.Version); c != 0 {
		return c
	}
	if c := cmp.Compare(t.Build, o.Build); c != 0 {
		return c
	}
	switch {
	case t.Pre == o.Pre:
		return 0
	case t.Pre == "":
		return 1
	case o.Pre == "":
		return -1
	}
	return comparePre(t.Pre, o.Pre)
}

// SameLine reports whether o could replace t in a version line: same
// variant, same precision (17.4 moves to 17.11, not 17.11.1, and 27 not to
// the build 27_35), the same major version unless crossMajor, and a release
// unless o is a later prerelease of t's own version (18beta3 to 18rc1, but
// 3.14.0rc1 never to 3.15.0a1).
func (t TagName) SameLine(o TagName, crossMajor bool) bool {
	return t.HasVersion() && o.HasVersion() &&
		o.Variant == t.Variant &&
		len(o.Version) == len(t.Version) &&
		(o.Build == 0) == (t.Build == 0) &&
		(crossMajor || o.Version[0] == t.Version[0]) &&
		(o.Pre == "" || t.Pre != "" && slices.Equal(o.Version, t.Version) && o.Build == t.Build)
}

// isPrereleaseSegment reports whether one "-"-separated part of a tag marks
// a prerelease: a word IsPrerelease knows, optionally numbered (rc, rc3,
// rc.1, dev3, beta2), or a single letter that only counts with a number
// (b4, a1, m03).
func isPrereleaseSegment(seg string) bool {
	seg = strings.ToLower(seg)
	i := 0
	for i < len(seg) && seg[i] >= 'a' && seg[i] <= 'z' {
		i++
	}
	word, num := seg[:i], seg[i:]
	if word == "" {
		return false
	}
	if isPrereleaseLetter(word) {
		return num != "" && isAllDigits(num)
	}
	num = strings.TrimPrefix(num, ".")
	return isPrereleaseWord(word) && (num == "" || isAllDigits(num)) && !strings.HasSuffix(seg, ".")
}

// comparePre orders prerelease markers by their first segment's stage
// (alpha < beta < rc; dev and nightly builds before all three), then by its
// number, then as text so the order is total.
func comparePre(a, b string) int {
	sa, _, _ := strings.Cut(a, "-")
	sb, _, _ := strings.Cut(b, "-")
	wa, na := splitPre(sa)
	wb, nb := splitPre(sb)
	if c := cmp.Compare(preStage(wa), preStage(wb)); c != 0 {
		return c
	}
	if c := cmp.Compare(na, nb); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

func splitPre(seg string) (word string, num int) {
	i := 0
	for i < len(seg) && seg[i] >= 'a' && seg[i] <= 'z' {
		i++
	}
	num, _ = strconv.Atoi(strings.TrimPrefix(seg[i:], "."))
	return seg[:i], num
}

func preStage(word string) int {
	switch word {
	case "a", "alpha":
		return 1
	case "b", "beta":
		return 2
	case "pre", "preview":
		return 3
	case "rc":
		return 4
	}
	return 0
}

// cutSegment splits s at its first "-", keeping the "-" on rest.
func cutSegment(s string) (seg, rest string) {
	if i := strings.IndexByte(s, '-'); i >= 0 {
		return s[:i], s[i:]
	}
	return s, ""
}

func leadingASCIIDigits(s string) (digits, rest string) {
	i := 0
	for i < len(s) && isASCIIDigit(s[i]) {
		i++
	}
	return s[:i], s[i:]
}

func isAllDigits(s string) bool {
	d, rest := leadingASCIIDigits(s)
	return d != "" && rest == ""
}

func isASCIIDigit(b byte) bool { return b >= '0' && b <= '9' }
