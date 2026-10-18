package hub

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Every name here is a real tag from Docker Hub.
func TestParseTagName(t *testing.T) {
	tests := []struct {
		name    string
		version []int
		build   int
		pre     string
		variant string
	}{
		{"17", []int{17}, 0, "", ""},
		{"17.4-alpine3.21", []int{17, 4}, 0, "", "alpine3.21"},
		{"1.31.4-alpine3.24-slim", []int{1, 31, 4}, 0, "", "alpine3.24-slim"},
		{"22.23.2-trixie-slim", []int{22, 23, 2}, 0, "", "trixie-slim"},
		{"4.2.5-management-alpine", []int{4, 2, 5}, 0, "", "management-alpine"},
		{"3.7.14-nanoserver-ltsc2022", []int{3, 7, 14}, 0, "", "nanoserver-ltsc2022"},
		{"v3.7.14-nanoserver-ltsc2022", []int{3, 7, 14}, 0, "", "nanoserver-ltsc2022"},
		{"v3-nanoserver-ltsc2025", []int{3}, 0, "", "nanoserver-ltsc2025"},
		{"24.04", []int{24, 4}, 0, "", ""},
		{"2024.10.1", []int{2024, 10, 1}, 0, "", ""},
		{"21.0.12.1_1-jdk-noble", []int{21, 0, 12, 1}, 1, "", "jdk-noble"},

		// Prereleases in every form Hub has them.
		{"8.6.0RC3-fpm-bookworm", []int{8, 6, 0}, 0, "rc3", "fpm-bookworm"},
		{"3.15.0b4", []int{3, 15, 0}, 0, "b4", ""},
		{"18beta3-alpine3.22", []int{18}, 0, "beta3", "alpine3.22"},
		{"8.4-rc-bookworm", []int{8, 4}, 0, "rc", "bookworm"},
		{"3.5-dev3-alpine3.24", []int{3, 5}, 0, "dev3", "alpine3.24"},
		{"v3.15.0-rc.0-busybox", []int{3, 15, 0}, 0, "rc.0", "busybox"},
		{"4.1.0-rc.1", []int{4, 1, 0}, 0, "rc.1", ""},
		{"12.1-ubi10-rc", []int{12, 1}, 0, "rc", "ubi10"},
		{"3.17.0_rc1", []int{3, 17, 0}, 0, "rc1", ""},
		{"8.8-m03", []int{8, 8}, 0, "m03", ""},
		{"v2.43.0-rc.1_stringlabels", []int{2, 43, 0}, 0, "rc.1", "stringlabels"},

		// Not versions: the parser declines rather than guesses.
		{"latest", nil, 0, "", ""},
		{"alpine3.24", nil, 0, "", ""},
		{"noble-20260911", nil, 0, "", ""},
		{"tip-20261010-alpine", nil, 0, "", ""},
		{"20260805", nil, 0, "", ""},
		{"8u412-jdk", nil, 0, "", ""},
		{"1a2b3c4d", nil, 0, "", ""},
		{"1.", nil, 0, "", ""},
		{"1.2.x", nil, 0, "", ""},
		{"v", nil, 0, "", ""},
		{"", nil, 0, "", ""},
		{"1234567890", nil, 0, "", ""},
	}
	for _, tt := range tests {
		got := ParseTagName(tt.name)
		if got.Name != tt.name || !slices.Equal(got.Version, tt.version) || got.Build != tt.build || got.Pre != tt.pre || got.Variant != tt.variant {
			t.Errorf("ParseTagName(%q) = %+v, want version %v build %d pre %q variant %q",
				tt.name, got, tt.version, tt.build, tt.pre, tt.variant)
		}
	}
}

func TestTagNameCompare(t *testing.T) {
	// Ascending; each name must sort after the one before it.
	ordered := []string{
		"3.14.9",
		"3.15.0-dev3",
		"3.15.0a1",
		"3.15.0b4",
		"3.15.0rc1",
		"3.15.0rc2",
		"3.15.0",
		"3.15.0_1",
		"3.15.0_10",
		"3.15.1",
		"v3.16",
	}
	for i := 1; i < len(ordered); i++ {
		a, b := ParseTagName(ordered[i-1]), ParseTagName(ordered[i])
		if a.Compare(b) >= 0 || b.Compare(a) <= 0 {
			t.Errorf("%s should sort before %s", a.Name, b.Name)
		}
	}
	if c := ParseTagName("17.4-alpine").Compare(ParseTagName("17.4-trixie")); c != 0 {
		t.Errorf("the variant took part in the comparison: %d", c)
	}
	if c := ParseTagName("3.15.0rc1").Compare(ParseTagName("3.15.0-rc1")); c != 0 {
		t.Errorf("rc1 written two ways compared as %d", c)
	}
}

func TestTagNameSameLine(t *testing.T) {
	tests := []struct {
		from, to   string
		crossMajor bool
		want       bool
	}{
		{"17.4-alpine", "17.11-alpine", false, true},
		{"17.4-alpine", "17.4-alpine", false, true},
		{"17.4-alpine", "17.11-alpine3.24", false, false}, // variant differs
		{"17.4-alpine", "17.11.1-alpine", false, false},   // precision differs
		{"17.4-alpine", "18.1-alpine", false, false},      // major differs
		{"17.4-alpine", "18.1-alpine", true, true},
		{"17-alpine", "18-alpine", true, true},
		{"17.4-alpine", "17.5rc1-alpine", false, false}, // a release never moves to a prerelease
		{"18beta3-alpine", "18rc1-alpine", false, true}, // a prerelease may move on
		{"18beta3-alpine", "18-alpine", false, true},
		{"latest", "latest", false, false},
		{"17.4", "latest", true, false},
	}
	for _, tt := range tests {
		if got := ParseTagName(tt.from).SameLine(ParseTagName(tt.to), tt.crossMajor); got != tt.want {
			t.Errorf("SameLine(%s → %s, crossMajor=%v) = %v, want %v", tt.from, tt.to, tt.crossMajor, got, tt.want)
		}
	}
}

// tag_names.txt holds every recent tag of 20 popular images, recorded
// 2026-10-18. The `u` filter and the parser must agree on which versioned
// tags are prereleases, or resolve --latest could pick a tag `u` hides.
func TestParseTagNameCorpus(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "testdata", "tag_names.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	var versioned, total int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name := sc.Text()
		total++
		tn := ParseTagName(name)
		if !tn.HasVersion() {
			continue
		}
		versioned++
		if got, want := tn.Pre != "", IsPrerelease(name); got != want {
			t.Errorf("%s: parser says prerelease=%v (pre %q), IsPrerelease says %v", name, got, tn.Pre, want)
		}
		// The parts must account for the whole name, so nothing is lost
		// when a caller matches on variant.
		if tn.Variant != "" && !strings.Contains(name, tn.Variant) {
			t.Errorf("%s: variant %q is not part of the name", name, tn.Variant)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	// Most tags of these images are versioned; a parser that declined
	// everything would pass the checks above vacuously.
	if versioned < total*3/4 {
		t.Errorf("only %d of %d tags parsed as versions", versioned, total)
	}
}
