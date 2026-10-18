package hub

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func namedTags(names ...string) []Tag {
	var tags []Tag
	for _, n := range names {
		tags = append(tags, Tag{Name: n, Digest: "sha256:" + strings.Repeat("b", 64), MediaType: "application/vnd.oci.image.index.v1+json"})
	}
	return tags
}

// resolve pins whatever LatestInLine picks, so a newest tag that cannot be
// pinned must give way to the newest one that can.
func TestLatestInLineSkipsUnpinnable(t *testing.T) {
	tags := namedTags("9.6.24", "9.6.23", "9.6.22", "9.6.20")
	tags[0].Digest = ""                                                             // legacy tag Hub reports no digest for
	tags[1].MediaType = "application/vnd.docker.distribution.manifest.v1+prettyjws" // schema 1
	reg := &lineRegistry{tags: tags, pageSize: 100}
	res, err := LatestInLine(context.Background(), reg, Repo{"library", "postgres"}, "9.6.20", LineOptions{})
	if err != nil || res.Tag.Name != "9.6.22" {
		t.Errorf("got %s, %v; want 9.6.22", res.Tag.Name, err)
	}

	// A line with nothing pinnable exists; saying "no tag" would be wrong.
	reg = &lineRegistry{tags: tags[:2], pageSize: 100}
	if _, err := LatestInLine(context.Background(), reg, Repo{"library", "postgres"}, "9.6.20", LineOptions{}); !errors.Is(err, ErrNoDigest) {
		t.Errorf("err = %v, want ErrNoDigest", err)
	}
}

// A prerelease may move on to a later prerelease of the same version or to
// any release, but not to a prerelease of some newer version: 3.14.0rc1
// wants 3.14.x, not the alpha of 3.15.
func TestLatestInLineFromPrerelease(t *testing.T) {
	tests := []struct {
		tags []string
		from string
		want string
	}{
		{[]string{"3.15.0a1", "3.14.2", "3.14.0rc2", "3.14.0rc1"}, "3.14.0rc1", "3.14.2"},
		{[]string{"3.15.0a1", "3.14.0rc2", "3.14.0rc1"}, "3.14.0rc1", "3.14.0rc2"},
		{[]string{"3.15.0a1", "3.14.0rc1"}, "3.14.0rc1", "3.14.0rc1"},
		{[]string{"19rc1-alpine", "18-alpine", "18rc1-alpine", "18beta3-alpine"}, "18beta3-alpine", "18-alpine"},
	}
	for _, tt := range tests {
		reg := &lineRegistry{tags: namedTags(tt.tags...), pageSize: 100}
		res, err := LatestInLine(context.Background(), reg, Repo{"library", "python"}, tt.from, LineOptions{})
		if err != nil || res.Tag.Name != tt.want {
			t.Errorf("%s in %v: got %s, %v; want %s", tt.from, tt.tags, res.Tag.Name, err, tt.want)
		}
	}
}

// When paging stops before any candidate is seen, "no tag in the line"
// would be wrong: the line may well exist past the cap.
func TestLatestInLineNothingBeforeCap(t *testing.T) {
	var names []string
	for range 5 {
		names = append(names, "17.0-other")
	}
	names = append(names, "17.11-alpine", "17.4-alpine")
	reg := &lineRegistry{tags: namedTags(names...), pageSize: 2, pageLimit: 2}
	_, err := LatestInLine(context.Background(), reg, Repo{"library", "postgres"}, "17.4-alpine", LineOptions{})
	if !errors.Is(err, ErrPageLimit) {
		t.Errorf("err = %v, want ErrPageLimit", err)
	}
}

// LatestInLine against a brute-force answer over every recorded tag name,
// so the server-side filter and the selection are checked together.
func TestLatestInLineCorpus(t *testing.T) {
	names := corpusNames(t)
	tags := namedTags(names...)
	parsed := make([]TagName, len(names))
	for i, n := range names {
		parsed[i] = ParseTagName(n)
	}
	// Large pages keep the fake from rescanning the corpus per page;
	// TestLatestInLinePaging covers paging.
	reg := &lineRegistry{tags: tags, pageSize: 2000}
	ctx := context.Background()

	checked := 0
	for i := 0; i < len(names); i += 23 {
		from := parsed[i]
		if !from.HasVersion() {
			continue
		}
		for _, cross := range []bool{false, true} {
			var want *TagName
			for j := range parsed {
				if from.SameLine(parsed[j], cross) && (want == nil || parsed[j].Compare(*want) > 0) {
					want = &parsed[j]
				}
			}
			res, err := LatestInLine(ctx, reg, Repo{"library", "x"}, from.Name, LineOptions{CrossMajor: cross})
			if err != nil {
				t.Fatalf("%s (cross %v): %v", from.Name, cross, err)
			}
			got := ParseTagName(res.Tag.Name)
			if !from.SameLine(got, cross) || got.Compare(*want) != 0 || got.Compare(from) < 0 {
				t.Errorf("%s (cross %v): got %s, want %s", from.Name, cross, got.Name, want.Name)
			}
			checked++
		}
	}
	if checked < 300 {
		t.Errorf("only %d lines checked", checked)
	}
}

// Picking a maximum is only meaningful if Compare is a total order:
// antisymmetric and transitive over real names of one variant.
func TestTagNameCompareIsOrder(t *testing.T) {
	byVariant := map[string][]TagName{}
	for _, n := range corpusNames(t) {
		if tn := ParseTagName(n); tn.HasVersion() {
			byVariant[tn.Variant] = append(byVariant[tn.Variant], tn)
		}
	}
	for v, tns := range byVariant {
		if len(tns) > 120 {
			tns = tns[:120]
		}
		for _, a := range tns {
			for _, b := range tns {
				ab := a.Compare(b)
				if ab != -b.Compare(a) {
					t.Fatalf("%s vs %s is not antisymmetric", a.Name, b.Name)
				}
				if ab > 0 {
					for _, c := range tns {
						if b.Compare(c) > 0 && a.Compare(c) <= 0 {
							t.Fatalf("variant %q: %s > %s > %s but not %s > %s", v, a.Name, b.Name, c.Name, a.Name, c.Name)
						}
					}
				}
			}
		}
	}
}

// Every real tag name, pinned and parsed back, gives the same parts: the
// reference resolve prints is one docker and hubtui both read.
func TestParseReferenceRoundTrip(t *testing.T) {
	digest := "sha256:" + strings.Repeat("c", 64)
	for _, repo := range []Repo{{"library", "postgres"}, {"grafana", "grafana"}} {
		for _, n := range corpusNames(t) {
			pinned, err := PinnedReference(repo, Tag{Name: n, Digest: digest})
			if err != nil {
				t.Fatalf("%s: %v", n, err)
			}
			got, err := ParseReference(pinned)
			if err != nil || got != (ImageRef{Repo: repo, Tag: n, Digest: digest}) {
				t.Fatalf("%s: parsed back as %+v, %v", pinned, got, err)
			}
		}
	}
}

func corpusNames(t *testing.T) []string {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "testdata", "tag_names.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var names []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		names = append(names, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}
