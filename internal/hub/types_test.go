package hub

import (
	"slices"
	"testing"
)

func TestAliases(t *testing.T) {
	const a, b = "sha256:aaaa", "sha256:bbbb"
	tags := []Tag{
		{Name: "latest", Digest: a},
		{Name: "1.31.6-alpine", Digest: b},
		{Name: "1.31", Digest: a},
		{Name: "old", Digest: ""},
		{Name: "older", Digest: ""},
		{Name: "mainline", Digest: a},
	}
	tests := []struct {
		name string
		tag  Tag
		want []string
	}{
		{"shared digest, input order, self excluded", tags[0], []string{"1.31", "mainline"}},
		{"unique digest", tags[1], nil},
		{"no digest never matches other empty digests", tags[3], nil},
		{"tag not in the list", Tag{Name: "1", Digest: a}, []string{"latest", "1.31", "mainline"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Aliases(tags, tt.tag); !slices.Equal(got, tt.want) {
				t.Errorf("Aliases(%s) = %q, want %q", tt.tag.Name, got, tt.want)
			}
		})
	}
}
