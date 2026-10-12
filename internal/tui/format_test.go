package tui

import (
	"slices"
	"strings"
	"testing"
)

func TestNaturalLess(t *testing.T) {
	names := []string{"1.10", "latest", "1.9.1", "1.9", "1.10-alpine", "1", "1.2", "01.3", "alpine3.20", "alpine3.9"}
	slices.SortFunc(names, func(a, b string) int {
		switch {
		case naturalLess(a, b):
			return -1
		case naturalLess(b, a):
			return 1
		}
		return 0
	})
	want := "1,1.2,01.3,1.9,1.9.1,1.10,1.10-alpine,alpine3.9,alpine3.20,latest"
	if got := strings.Join(names, ","); got != want {
		t.Errorf("sorted = %s\nwant     %s", got, want)
	}
}
