// Package dockerfile reads the FROM instructions of a Dockerfile and
// rewrites their image references in place. It knows nothing about
// registries; it only finds what the file says and where.
package dockerfile

import (
	"bytes"
	"cmp"
	"regexp"
	"slices"
	"strings"
)

// From is one FROM instruction.
type From struct {
	// Line is the 1-based line the image reference is on.
	Line int
	// Image is the reference as written, before any build-arg expansion.
	Image string
	// Platform is the --platform flag's value as written; empty without one.
	Platform string
	// Stage is the AS name; empty when the stage is unnamed.
	Stage string
	// Start and End are Image's byte offsets in the source, for Rewrite.
	// Start is -1 when the reference is split across a line continuation
	// and cannot be replaced as one span.
	Start, End int
}

// Edit replaces the image reference of one From.
type Edit struct {
	From  From
	Image string
}

// heredocRE finds heredoc openers such as <<EOF, <<-EOF and <<"EOF".
var heredocRE = regexp.MustCompile(`<<(-?)(["']?)([A-Za-z_][A-Za-z0-9_]*)(["']?)`)

// Parse returns the file's FROM instructions in order.
//
// It follows the parts of the Dockerfile grammar that decide where an
// instruction starts: the escape parser directive, line continuations,
// comment lines (also inside continuations) and heredoc bodies, whose
// lines are script, never instructions.
func Parse(src []byte) []From {
	lines := splitLines(src)
	escape := escapeDirective(lines)

	var froms []From
	for i := 0; i < len(lines); {
		first := i
		text := lines[i].text
		trimmed := strings.TrimSpace(text)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			i++
			continue
		}

		// Join continuation lines, remembering where each logical byte
		// came from so the image can be mapped back to the file.
		var logical []byte
		var offsets []int
		for {
			body := lines[i].text
			end := len(strings.TrimRight(body, " \t"))
			cont := end > 0 && body[end-1] == escape
			if cont {
				end--
			}
			for j := 0; j < end; j++ {
				logical = append(logical, body[j])
				offsets = append(offsets, lines[i].start+j)
			}
			i++
			if !cont {
				break
			}
			// Comment and blank lines inside a continuation are dropped.
			for i < len(lines) {
				t := strings.TrimSpace(lines[i].text)
				if t != "" && !strings.HasPrefix(t, "#") {
					break
				}
				i++
			}
			if i >= len(lines) {
				break
			}
		}

		if f, ok := parseFrom(string(logical), offsets, lines, first); ok {
			froms = append(froms, f)
		}
		i = skipHeredocs(string(logical), lines, i)
	}
	return froms
}

// Rewrite applies edits to src. Edits whose From cannot be rewritten
// (Start < 0) or no longer matches src are skipped; the result reports
// how many were applied.
func Rewrite(src []byte, edits []Edit) ([]byte, int) {
	var out bytes.Buffer
	pos, applied := 0, 0
	for _, e := range sortedEdits(edits) {
		f := e.From
		if f.Start < pos || f.End > len(src) || string(src[f.Start:f.End]) != f.Image {
			continue
		}
		out.Write(src[pos:f.Start])
		out.WriteString(e.Image)
		pos = f.End
		applied++
	}
	out.Write(src[pos:])
	return out.Bytes(), applied
}

func sortedEdits(edits []Edit) []Edit {
	out := make([]Edit, 0, len(edits))
	for _, e := range edits {
		if e.From.Start >= 0 {
			out = append(out, e)
		}
	}
	slices.SortStableFunc(out, func(a, b Edit) int { return cmp.Compare(a.From.Start, b.From.Start) })
	return out
}

type line struct {
	text  string // without the line ending
	start int    // byte offset of text in the source
}

// splitLines keeps \r out of the text so CRLF files parse like LF files,
// while offsets still point into the original bytes.
func splitLines(src []byte) []line {
	var lines []line
	start := 0
	for start <= len(src) {
		end := bytes.IndexByte(src[start:], '\n')
		next := 0
		if end < 0 {
			end = len(src)
			next = len(src) + 1
		} else {
			end += start
			next = end + 1
		}
		text := string(src[start:end])
		text = strings.TrimSuffix(text, "\r")
		lines = append(lines, line{text: text, start: start})
		start = next
	}
	return lines
}

// escapeDirective reads "# escape=`" from the parser directives, which
// must come before anything else in the file.
func escapeDirective(lines []line) byte {
	for _, l := range lines {
		t := strings.TrimSpace(l.text)
		if !strings.HasPrefix(t, "#") {
			break
		}
		key, value, ok := strings.Cut(strings.TrimSpace(t[1:]), "=")
		if !ok {
			break
		}
		if strings.EqualFold(strings.TrimSpace(key), "escape") {
			if v := strings.TrimSpace(value); v == "`" {
				return '`'
			}
			return '\\'
		}
	}
	return '\\'
}

func parseFrom(logical string, offsets []int, lines []line, first int) (From, bool) {
	words := fields(logical)
	if len(words) == 0 || !strings.EqualFold(words[0].text, "FROM") {
		return From{}, false
	}
	f := From{Start: -1}
	rest := words[1:]
	for len(rest) > 0 && strings.HasPrefix(rest[0].text, "--") {
		if v, ok := strings.CutPrefix(rest[0].text, "--platform="); ok {
			f.Platform = v
		}
		rest = rest[1:]
	}
	if len(rest) == 0 {
		// FROM with no image; docker rejects it, so there is nothing to check.
		return From{}, false
	}
	img := rest[0]
	f.Image = img.text
	if len(rest) >= 3 && strings.EqualFold(rest[1].text, "AS") {
		f.Stage = rest[2].text
	}

	start, end := offsets[img.start], offsets[img.end-1]+1
	if end-start == img.end-img.start {
		f.Start, f.End = start, end
	}
	f.Line = lineOf(lines, first, offsets[img.start])
	return f, true
}

func lineOf(lines []line, from, offset int) int {
	for i := from; i < len(lines); i++ {
		if i+1 == len(lines) || lines[i+1].start > offset {
			return i + 1
		}
	}
	return len(lines)
}

type word struct {
	text       string
	start, end int // in the logical line
}

func fields(s string) []word {
	var out []word
	i := 0
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		start := i
		for i < len(s) && s[i] != ' ' && s[i] != '\t' {
			i++
		}
		if i > start {
			out = append(out, word{s[start:i], start, i})
		}
	}
	return out
}

// skipHeredocs moves past the bodies of any heredocs the instruction
// opens, in order; each body ends at a line holding only its word (after
// leading tabs, for <<-).
func skipHeredocs(logical string, lines []line, i int) int {
	if strings.HasPrefix(strings.TrimSpace(logical), "#") {
		return i
	}
	for _, m := range heredocRE.FindAllStringSubmatch(logical, -1) {
		if m[2] != m[4] {
			continue // mismatched quotes: not a heredoc opener
		}
		stripTabs, word := m[1] == "-", m[3]
		for i < len(lines) {
			t := lines[i].text
			if stripTabs {
				t = strings.TrimLeft(t, "\t")
			}
			i++
			if t == word {
				break
			}
		}
	}
	return i
}
