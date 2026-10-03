package tui

import (
	"slices"
	"strings"
	"time"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

// groupLevel is how finely the Tags screen groups versions.
type groupLevel int

const (
	groupOff groupLevel = iota
	groupMajor
	groupMinor
	numGroupLevels
)

func (g groupLevel) String() string {
	return [...]string{"off", "major", "minor"}[g]
}

// otherGroup holds tags that do not start with a version: latest, alpine,
// mainline, codenames and date stamps.
const otherGroup = "other"

// groupKey is the version group a tag belongs to at level: "17" or "1.31"
// for "1.31.6-alpine". A tag with only a major version ("1-alpine") stays in
// its major group at the minor level; it floats across minors.
func groupKey(name string, level groupLevel) string {
	s := strings.ToLower(name)
	if len(s) > 1 && s[0] == 'v' && isDigit(s[1]) {
		s = s[1:]
	}
	major, rest := leadingDigits(s)
	// More than four digits is a date or build number, not a major version.
	if major == "" || len(major) > 4 {
		return otherGroup
	}
	major = trimZeros(major)
	if level == groupMajor || len(rest) < 2 || rest[0] != '.' || !isDigit(rest[1]) {
		return major
	}
	// The minor stays as written: Ubuntu's 24.04 is not 24.4.
	minor, _ := leadingDigits(rest[1:])
	return major + "." + minor
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// trimZeros keeps a zero-padded major ("07") with its plain form without
// turning "0" into "".
func trimZeros(s string) string {
	if t := strings.TrimLeft(s, "0"); t != "" {
		return t
	}
	return "0"
}

// tagGroup is one row of the grouped view.
type tagGroup struct {
	key string
	// tags indexes the screen's loaded tags, in the order given.
	tags []int
	// newest is the index of the most recently pushed tag.
	newest int
	pushed time.Time
}

// buildGroups groups the tags at idx by version, newest version first and
// the other group last.
func buildGroups(all []hub.Tag, idx []int, level groupLevel) []tagGroup {
	byKey := map[string]*tagGroup{}
	var keys []string
	for _, i := range idx {
		t := all[i]
		k := groupKey(t.Name, level)
		g, ok := byKey[k]
		if !ok {
			g = &tagGroup{key: k, newest: i, pushed: t.Pushed}
			byKey[k] = g
			keys = append(keys, k)
		}
		g.tags = append(g.tags, i)
		if t.Pushed.After(g.pushed) {
			g.newest, g.pushed = i, t.Pushed
		}
	}
	slices.SortFunc(keys, func(a, b string) int {
		switch {
		case a == b:
			return 0
		case a == otherGroup:
			return 1
		case b == otherGroup:
			return -1
		case naturalLess(b, a):
			return -1
		}
		return 1
	})
	out := make([]tagGroup, len(keys))
	for i, k := range keys {
		out[i] = *byKey[k]
	}
	return out
}
