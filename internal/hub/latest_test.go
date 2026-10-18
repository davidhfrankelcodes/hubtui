package hub

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lineRegistry serves tags newest first, applies Hub's substring name
// filter, and pages by pageSize. pageLimit > 0 fails pages past it the way
// anonymous paging does.
type lineRegistry struct {
	tags      []Tag
	pageSize  int
	pageLimit int
	listCalls []TagsOptions
	tagCalls  []string
}

func (r *lineRegistry) Search(context.Context, string, PageOptions) (*SearchPage, error) {
	return nil, errors.New("not used")
}

func (r *lineRegistry) Tags(_ context.Context, _ Repo, opts TagsOptions) (*TagPage, error) {
	r.listCalls = append(r.listCalls, opts)
	if r.pageLimit > 0 && opts.Page > r.pageLimit {
		return nil, &APIError{StatusCode: 403, Message: "pagination limit too large"}
	}
	var match []Tag
	for _, t := range r.tags {
		if strings.Contains(t.Name, opts.Name) {
			match = append(match, t)
		}
	}
	start := min((opts.Page-1)*r.pageSize, len(match))
	end := min(start+r.pageSize, len(match))
	return &TagPage{Tags: match[start:end], Page: opts.Page, HasNext: end < len(match)}, nil
}

func (r *lineRegistry) Tag(_ context.Context, repo Repo, name string) (*Tag, error) {
	r.tagCalls = append(r.tagCalls, name)
	for _, t := range r.tags {
		if t.Name == name {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("getting %s:%s: %w", repo, name, ErrNotFound)
}

// postgresTags mimics postgres: newest pushed first, several variants per
// version, and prereleases of the next major.
func postgresTags() []Tag {
	amd := []Platform{{OS: "linux", Arch: "amd64"}}
	both := []Platform{{OS: "linux", Arch: "amd64"}, {OS: "linux", Arch: "arm64", Variant: "v8"}}
	var tags []Tag
	for _, n := range []string{
		"19rc1-alpine", "19rc1", "18.1-alpine", "18.1", "18-alpine", "18",
		"17.11-alpine3.24", "17.11-alpine", "17.11", "17-alpine", "17",
		"17.10-alpine", "17.9-alpine", "16.12-alpine", "18beta3-alpine", "18rc1-alpine",
		"17.4-alpine", "latest", "alpine",
	} {
		tags = append(tags, Tag{Name: n, Digest: "sha256:" + strings.Repeat("a", 64), Platforms: both})
	}
	// The newest 17.x Alpine build lacks arm64, so a platform filter must
	// fall back to the one before it.
	tags[7].Platforms = amd
	return tags
}

func TestLatestInLine(t *testing.T) {
	postgres := Repo{"library", "postgres"}
	tests := []struct {
		name       string
		from       string
		opts       LineOptions
		want       string
		wantFilter string // "-" for no listing at all
		wantErr    error
	}{
		{name: "minor moves within major", from: "17.4-alpine", want: "17.11-alpine", wantFilter: "17."},
		{name: "already newest", from: "17.11-alpine", want: "17.11-alpine", wantFilter: "17."},
		{name: "variant must match exactly", from: "17.4", want: "17.11", wantFilter: "17."},
		{name: "platform skips the newest", from: "17.4-alpine", opts: LineOptions{Platform: "arm64"}, want: "17.10-alpine", wantFilter: "17."},
		{name: "one part within its major is itself", from: "17-alpine", want: "17-alpine", wantFilter: "-"},
		{name: "cross major", from: "17-alpine", opts: LineOptions{CrossMajor: true}, want: "18-alpine", wantFilter: "alpine"},
		{name: "cross major never picks a prerelease", from: "17.4-alpine", opts: LineOptions{CrossMajor: true}, want: "18.1-alpine", wantFilter: "alpine"},
		{name: "cross major without variant", from: "17.4", opts: LineOptions{CrossMajor: true}, want: "18.1", wantFilter: ""},
		{name: "prerelease moves to its release", from: "18beta3-alpine", want: "18-alpine", wantFilter: "18"},
		{name: "no version", from: "latest", wantErr: ErrNoVersion},
		{name: "nothing has the platform", from: "17.4-alpine", opts: LineOptions{Platform: "s390x"}, wantErr: ErrNotFound},
		{name: "one-part tag missing the platform", from: "17-alpine", opts: LineOptions{Platform: "s390x"}, wantErr: ErrNotFound},
		{name: "one-part tag that does not exist", from: "15-alpine", wantErr: ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := &lineRegistry{tags: postgresTags(), pageSize: 100}
			res, err := LatestInLine(context.Background(), reg, postgres, tt.from, tt.opts)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if res.Tag.Name != tt.want || res.Partial {
				t.Errorf("got %s (partial %v), want %s", res.Tag.Name, res.Partial, tt.want)
			}
			switch {
			case tt.wantFilter == "-" && len(reg.listCalls) != 0:
				t.Errorf("listed tags %d times; one lookup should do", len(reg.listCalls))
			case tt.wantFilter != "-" && (len(reg.listCalls) != 1 || reg.listCalls[0].Name != tt.wantFilter):
				t.Errorf("list calls = %+v, want one filtered by %q", reg.listCalls, tt.wantFilter)
			}
		})
	}
}

func TestLatestInLinePaging(t *testing.T) {
	ctx := context.Background()
	postgres := Repo{"library", "postgres"}

	// Every page is read: the newest version can be pushed after others.
	reg := &lineRegistry{tags: postgresTags(), pageSize: 2}
	res, err := LatestInLine(ctx, reg, postgres, "17.4", LineOptions{CrossMajor: true})
	if err != nil || res.Tag.Name != "18.1" || len(reg.listCalls) != 10 {
		t.Errorf("got %s, %v after %d pages", res.Tag.Name, err, len(reg.listCalls))
	}

	// Hitting the anonymous cap returns the best so far, marked partial.
	reg = &lineRegistry{tags: postgresTags(), pageSize: 2, pageLimit: 2}
	res, err = LatestInLine(ctx, reg, postgres, "17.4", LineOptions{CrossMajor: true})
	if err != nil || res.Tag.Name != "18.1" || !res.Partial {
		t.Errorf("got %s, partial %v, err %v", res.Tag.Name, res.Partial, err)
	}

	// Other errors are errors, not partial answers.
	reg = &lineRegistry{tags: postgresTags(), pageSize: 2}
	failing := &failingTags{lineRegistry: reg, err: errors.New("boom")}
	if _, err := LatestInLine(ctx, failing, postgres, "17.4-alpine", LineOptions{}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v", err)
	}
}

type failingTags struct {
	*lineRegistry
	err error
}

func (f *failingTags) Tags(context.Context, Repo, TagsOptions) (*TagPage, error) { return nil, f.err }

// The server-side filter must be part of every tag SameLine would accept,
// or Hub drops a candidate before it is ever compared. Checked over every
// recorded tag name, within and across majors.
func TestLineFilterKeepsEveryCandidate(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "testdata", "tag_names.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	// Candidates share variant and precision, so bucket on those to avoid
	// comparing every pair.
	type key struct {
		variant string
		parts   int
	}
	buckets := map[key][]TagName{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if tn := ParseTagName(sc.Text()); tn.HasVersion() {
			k := key{tn.Variant, len(tn.Version)}
			buckets[k] = append(buckets[k], tn)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	checked := 0
	for _, names := range buckets {
		for _, base := range names {
			for _, cross := range []bool{false, true} {
				filter := lineFilter(base, cross)
				for _, c := range names {
					if base.SameLine(c, cross) {
						checked++
						if !strings.Contains(c.Name, filter) {
							t.Errorf("line of %s (crossMajor %v): filter %q drops %s", base.Name, cross, filter, c.Name)
						}
					}
				}
			}
		}
	}
	if checked < 10000 {
		t.Errorf("only %d pairs checked", checked)
	}
}
