package hub

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func checkDigest(i int) string { return fmt.Sprintf("sha256:%064x", i) }

// checkTags: each tag's index digest is checkDigest(i), and its arm64
// image is checkDigest(1000+i).
func checkTags() []Tag {
	var tags []Tag
	for i, n := range []string{"17.11-alpine", "17.10-alpine", "17.4-alpine", "17-alpine", "18-alpine", "alpine", "24.04", "9.6.24", "9.6.22"} {
		tags = append(tags, Tag{
			Name:      n,
			Digest:    checkDigest(i),
			MediaType: "application/vnd.oci.image.index.v1+json",
			Platforms: []Platform{
				{OS: "linux", Arch: "amd64", Digest: checkDigest(500 + i)},
				{OS: "linux", Arch: "arm64", Variant: "v8", Digest: checkDigest(1000 + i)},
			},
		})
	}
	tags[0].Platforms = tags[0].Platforms[:1] // newest 17.x has no arm64
	tags[7].Digest = ""                       // legacy 9.6.24
	return tags
}

func TestCheckReference(t *testing.T) {
	tests := []struct {
		name          string
		ref           string
		opts          LineOptions
		wantStatus    CheckStatus
		wantSuggested string
		wantErr       string
	}{
		{name: "newer in line", ref: "postgres:17.4-alpine@" + checkDigest(2), wantStatus: CheckOutdated, wantSuggested: "17.11-alpine"},
		{name: "newer, unpinned too", ref: "postgres:17.4-alpine", wantStatus: CheckOutdated, wantSuggested: "17.11-alpine"},
		{name: "platform picks the newest with it", ref: "postgres:17.4-alpine", opts: LineOptions{Platform: "linux/arm64"}, wantStatus: CheckOutdated, wantSuggested: "17.10-alpine"},
		{name: "current", ref: "postgres:17.11-alpine@" + checkDigest(0), wantStatus: CheckOK},
		{name: "current but rebuilt", ref: "postgres:17.11-alpine@" + checkDigest(77), wantStatus: CheckMoved, wantSuggested: "17.11-alpine"},
		{name: "current, unpinned", ref: "postgres:17.11-alpine", wantStatus: CheckUnpinned, wantSuggested: "17.11-alpine"},
		{name: "pinned to the amd64 image", ref: "postgres:17.11-alpine@" + checkDigest(500), wantStatus: CheckSingleArch, wantSuggested: "17.11-alpine"},
		{name: "one-part tag is its own line", ref: "postgres:17-alpine@" + checkDigest(3), wantStatus: CheckOK},
		{name: "major crosses with opts", ref: "postgres:17-alpine@" + checkDigest(3), opts: LineOptions{CrossMajor: true}, wantStatus: CheckOutdated, wantSuggested: "18-alpine"},
		{name: "unversioned tag only checks the pin", ref: "postgres:alpine@" + checkDigest(77), wantStatus: CheckMoved, wantSuggested: "alpine"},
		{name: "unversioned, current", ref: "postgres:alpine@" + checkDigest(5), wantStatus: CheckOK},
		{name: "24.04 is not older than itself", ref: "ubuntu:24.04@" + checkDigest(6), wantStatus: CheckOK},
		{name: "legacy newest is passed over", ref: "postgres:9.6.22@" + checkDigest(8), wantStatus: CheckOK},

		{name: "tag missing", ref: "postgres:15-alpine", wantErr: "not found"},
		{name: "current lacks the platform", ref: "postgres:17.11-alpine", opts: LineOptions{Platform: "arm64"}, wantErr: "has no arm64 image"},
		{name: "no tag", ref: "postgres@" + checkDigest(1), wantErr: "without a tag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := ParseReference(tt.ref)
			if err != nil {
				t.Fatal(err)
			}
			reg := &lineRegistry{tags: checkTags(), pageSize: 100}
			res, err := CheckReference(context.Background(), reg, ref, tt.opts)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if res.Status != tt.wantStatus || res.Suggested.Name != tt.wantSuggested {
				t.Errorf("got %s → %q, want %s → %q", res.Status, res.Suggested.Name, tt.wantStatus, tt.wantSuggested)
			}
			// Every suggestion is something resolve could print.
			if res.Status != CheckOK {
				if _, err := PinnedReference(ref.Repo, res.Suggested); err != nil {
					t.Errorf("suggestion cannot be pinned: %v", err)
				}
			}
		})
	}
}

func TestCheckReferencePartial(t *testing.T) {
	reg := &lineRegistry{tags: checkTags(), pageSize: 1, pageLimit: 1}
	ref, _ := ParseReference("postgres:17.11-alpine@" + checkDigest(0))
	res, err := CheckReference(context.Background(), reg, ref, LineOptions{})
	if err != nil || res.Status != CheckOK || !res.Partial {
		t.Errorf("got %+v, %v; want ok and partial", res, err)
	}
}

func TestCheckReferenceUnpinnable(t *testing.T) {
	tags := checkTags()
	tags[5].MediaType = "application/vnd.docker.distribution.manifest.v1+prettyjws"
	reg := &lineRegistry{tags: tags, pageSize: 100}
	ref, _ := ParseReference("postgres:alpine")
	if _, err := CheckReference(context.Background(), reg, ref, LineOptions{}); !errors.Is(err, ErrNoDigest) {
		t.Errorf("err = %v, want ErrNoDigest", err)
	}
}
