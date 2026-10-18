package dockerfile

import (
	"slices"
	"strings"
	"testing"
)

type want struct {
	line     int
	image    string
	platform string
	stage    string
}

func got(froms []From) []want {
	var out []want
	for _, f := range froms {
		out = append(out, want{f.Line, f.Image, f.Platform, f.Stage})
	}
	return out
}

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []want
	}{
		{
			name: "multi-stage",
			src: `# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine@sha256:8a59 AS build
RUN go build ./...

from scratch
COPY --from=build /out /out
`,
			want: []want{{2, "golang:1.27.1-alpine@sha256:8a59", "$BUILDPLATFORM", "build"}, {5, "scratch", "", ""}},
		},
		{
			name: "lowercase as and extra spaces",
			src:  "  FROM\tpostgres:17   as   db\n",
			want: []want{{1, "postgres:17", "", "db"}},
		},
		{
			name: "continuation puts the image on the next line",
			src:  "FROM \\\n  --platform=linux/arm64 \\\n  # a comment inside the continuation\n\n  node:22-slim \\\n  AS web\n",
			want: []want{{5, "node:22-slim", "linux/arm64", "web"}},
		},
		{
			name: "escape directive",
			src:  "# escape=`\nFROM mcr.microsoft.com/windows/servercore:ltsc2022 `\n  AS base\nRUN dir C:\\\n",
			want: []want{{2, "mcr.microsoft.com/windows/servercore:ltsc2022", "", "base"}},
		},
		{
			// A backslash is a path separator under escape=`, not a continuation.
			name: "backslash under backtick escape",
			src:  "# escape=`\nRUN echo C:\\\nFROM alpine:3.22\n",
			want: []want{{3, "alpine:3.22", "", ""}},
		},
		{
			name: "directive after a comment is just a comment",
			src:  "# hello\n# escape=`\nFROM alpine `\n",
			want: []want{{3, "alpine", "", ""}},
		},
		{
			name: "heredoc bodies are not instructions",
			src: `FROM alpine:3.22
RUN <<EOF
FROM this is shell
EOF
COPY <<-"CONF" /etc/app.conf
	FROM also not
	CONF
FROM debian:trixie
`,
			want: []want{{1, "alpine:3.22", "", ""}, {8, "debian:trixie", "", ""}},
		},
		{
			name: "two heredocs on one line",
			src:  "FROM a\nRUN <<ONE cat - <<TWO\nFROM x\nONE\nFROM y\nTWO\nFROM b\n",
			want: []want{{1, "a", "", ""}, {7, "b", "", ""}},
		},
		{
			name: "commented-out FROM",
			src:  "# FROM old:1\nFROM new:2\n",
			want: []want{{2, "new:2", "", ""}},
		},
		{
			name: "CRLF",
			src:  "FROM alpine:3.22 AS a\r\nFROM \\\r\n  busybox\r\n",
			want: []want{{1, "alpine:3.22", "", "a"}, {3, "busybox", "", ""}},
		},
		{
			name: "build args",
			src:  "ARG GO=1.27\nFROM golang:${GO}-alpine\n",
			want: []want{{2, "golang:${GO}-alpine", "", ""}},
		},
		{
			name: "FROM without an image",
			src:  "FROM\nFROM --platform=linux/amd64\n",
			want: nil,
		},
		{
			name: "no trailing newline",
			src:  "FROM alpine",
			want: []want{{1, "alpine", "", ""}},
		},
		{name: "empty", src: "", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			froms := Parse([]byte(tt.src))
			if g := got(froms); !slices.Equal(g, tt.want) {
				t.Errorf("Parse = %+v\nwant    %+v", g, tt.want)
			}
			// The offsets must point at exactly the image as written.
			for _, f := range froms {
				if f.Start >= 0 && tt.src[f.Start:f.End] != f.Image {
					t.Errorf("offsets of %q point at %q", f.Image, tt.src[f.Start:f.End])
				}
			}
		})
	}
}

func TestRewrite(t *testing.T) {
	src := "FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build\r\n" +
		"RUN <<EOF\nFROM golang:1.27-alpine\nEOF\n" +
		"FROM \\\n  golang:1.27-alpine\n" +
		"FROM alpine # not a comment to docker, but kept as is\n"
	froms := Parse([]byte(src))
	if len(froms) != 3 {
		t.Fatalf("parsed %d FROMs", len(froms))
	}
	edits := []Edit{
		// Out of order on purpose.
		{From: froms[1], Image: "golang:1.28-alpine@sha256:abc"},
		{From: froms[0], Image: "golang:1.28-alpine@sha256:abc"},
	}
	out, n := Rewrite([]byte(src), edits)
	want := "FROM --platform=$BUILDPLATFORM golang:1.28-alpine@sha256:abc AS build\r\n" +
		"RUN <<EOF\nFROM golang:1.27-alpine\nEOF\n" +
		"FROM \\\n  golang:1.28-alpine@sha256:abc\n" +
		"FROM alpine # not a comment to docker, but kept as is\n"
	if string(out) != want || n != 2 {
		t.Errorf("Rewrite applied %d:\n%s\nwant:\n%s", n, out, want)
	}

	// A stale edit (the file changed since parsing) is skipped, not applied
	// at the wrong place.
	stale := froms[0]
	stale.Image = "golang:9"
	if out, n := Rewrite([]byte(src), []Edit{{From: stale, Image: "x"}}); n != 0 || string(out) != src {
		t.Errorf("stale edit applied: %d\n%s", n, out)
	}
}

// An image split by a continuation cannot be replaced as one span.
func TestSplitImageIsNotRewritable(t *testing.T) {
	src := "FROM golang:1.27\\\n-alpine\n"
	froms := Parse([]byte(src))
	if len(froms) != 1 || froms[0].Image != "golang:1.27-alpine" || froms[0].Start != -1 {
		t.Fatalf("froms = %+v", froms)
	}
	if out, n := Rewrite([]byte(src), []Edit{{From: froms[0], Image: "x"}}); n != 0 || string(out) != src {
		t.Errorf("rewrote a split image: %q", out)
	}
}

// Parse must never panic, and its offsets must always point at the image
// as written, or --write would corrupt the file.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"FROM a\n", "# escape=`\nFROM a `\n AS b\n", "FROM \\\n\\\nb\n", "RUN <<A <<-B\nA\n\tB\nFROM c",
		"FROM --platform=x\r\n", "FROM a\\", "\\\n", "FROM a\\\r\nb",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		for _, fr := range Parse([]byte(src)) {
			if fr.Start >= 0 && src[fr.Start:fr.End] != fr.Image {
				t.Fatalf("offsets of %q point at %q", fr.Image, src[fr.Start:fr.End])
			}
			if fr.Line < 1 || fr.Line > strings.Count(src, "\n")+1 {
				t.Fatalf("line %d out of range", fr.Line)
			}
		}
	})
}
