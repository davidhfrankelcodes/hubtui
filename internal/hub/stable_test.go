package hub

import "testing"

// The names come from live Docker Hub tags.
func TestIsPrerelease(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		// Releases, including words that contain a prerelease word.
		{"latest", false},
		{"1.31.6-alpine3.24", false},
		{"mainline-alpine-perl", false},
		{"stable-alpine", false},
		{"3.14.2-slim-trixie", false},
		{"17.6-bookworm", false},
		{"ltsc2025", false},
		{"jammy-20260911", false},
		{"20260918", false},
		{"12.2.0-ubuntu", false},
		{"8.8.0-alpine", false},
		{"lts-jdk21", false},
		{"v3.5.1", false},
		{"beef", false}, // too short to be a commit

		// Prereleases.
		{"3.15.0rc2", true},
		{"3.15-rc-trixie", true},
		{"3.15.0b4-slim", true},
		{"3.15.0a7", true},
		{"8.8-m03-alpine", true},
		{"1.17.0-alpha20260827", true},
		{"9.0.0-beta1", true},
		{"nightly-ubuntu", true},
		{"dev-preview-react19", true},
		{"tip-20260926-alpine", true},
		{"unstable-slim", true},
		{"sid-20260918", true},
		{"testing-backports", true},
		{"experimental", true},
		{"rc-buggy", true},
		{"edge", true},
		{"main", true},
		{"sha-1a2b3c4", true},
		{"1a2b3c4", true},
		{"0123456789abcdef0123456789abcdef01234567", true},
		{"3.15.0RC2", true},
	}
	for _, tt := range tests {
		if got := IsPrerelease(tt.name); got != tt.want {
			t.Errorf("IsPrerelease(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
