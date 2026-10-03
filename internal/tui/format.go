package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

// humanSize uses decimal units because that is what hub.docker.com shows,
// so numbers match when people compare.
func humanSize(n int64) string {
	if n <= 0 {
		return "-"
	}
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}

func relativeTime(t, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/24/30))
	default:
		return fmt.Sprintf("%dy ago", int(d.Hours()/24/365))
	}
}

// shortDigest matches the 12-character IDs docker prints elsewhere.
func shortDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if d == "" {
		return "-"
	}
	return d[:min(12, len(d))]
}

// archRank puts the platforms people usually look for first, so they survive
// truncation in a narrow column.
func archRank(s string) (int, bool) {
	switch s {
	case "amd64":
		return 0, true
	case "arm64", "arm64/v8":
		return 1, true
	case "arm/v7":
		return 2, true
	case "arm/v6":
		return 3, true
	case "386":
		return 4, true
	}
	return 0, false
}

// archSummary lists platforms compactly within width cells, ending with "+N"
// for any that do not fit. "linux/" is implied since nearly every image is
// Linux; other OSes stay visible.
func archSummary(ps []hub.Platform, width int) string {
	if len(ps) == 0 {
		return "-"
	}
	seen := map[string]bool{}
	var names []string
	for _, p := range ps {
		s := strings.TrimPrefix(p.String(), "linux/")
		if !seen[s] {
			seen[s] = true
			names = append(names, s)
		}
	}
	slices.SortStableFunc(names, func(a, b string) int {
		ra, okA := archRank(a)
		rb, okB := archRank(b)
		switch {
		case okA && okB:
			return ra - rb
		case okA:
			return -1
		case okB:
			return 1
		}
		return 0
	})

	out := ""
	for i, n := range names {
		next := n
		if out != "" {
			next = out + "," + n
		}
		more := ""
		if rest := len(names) - i - 1; rest > 0 {
			more = fmt.Sprintf(",+%d", rest)
		}
		if len(next)+len(more) > width {
			if out == "" {
				return n // nothing fits; let the table truncate it
			}
			return fmt.Sprintf("%s,+%d", out, len(names)-i)
		}
		out = next
	}
	return out
}

// displaySize picks one platform's size to stand for the tag. amd64 is the
// most common reference point; summing platforms would be meaningless since
// nobody pulls them all.
func displaySize(t hub.Tag) int64 {
	if len(t.Platforms) == 0 {
		return 0
	}
	for _, p := range t.Platforms {
		if p.Arch == "amd64" && (p.OS == "linux" || p.OS == "") {
			return p.Size
		}
	}
	return t.Platforms[0].Size
}

// naturalLess orders "1.9" before "1.10" by comparing digit runs as numbers.
func naturalLess(a, b string) bool {
	for a != "" && b != "" {
		ra, rb := rune(a[0]), rune(b[0])
		if unicode.IsDigit(ra) && unicode.IsDigit(rb) {
			na, restA := leadingDigits(a)
			nb, restB := leadingDigits(b)
			// Compare by length first so arbitrarily long numbers never overflow.
			ta, tb := strings.TrimLeft(na, "0"), strings.TrimLeft(nb, "0")
			if len(ta) != len(tb) {
				return len(ta) < len(tb)
			}
			if ta != tb {
				return ta < tb
			}
			a, b = restA, restB
			continue
		}
		if ra != rb {
			return ra < rb
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func leadingDigits(s string) (digits, rest string) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i], s[i:]
}
