package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/davidhfrankelcodes/hubtui/internal/format"
	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

// The tables are for people; scripts should use --json, whose shape is
// stable. Columns and formatting match the TUI so the two read the same.

const (
	// descriptionWidth keeps a search row on one line in a typical terminal.
	descriptionWidth = 60
	// platformsWidth fits the common platforms of a multi-arch image and
	// counts the rest, as the TUI's column does.
	platformsWidth = 40
)

func (a *app) searchTable(results []hub.SearchResult) int {
	rows := make([][]string, 0, len(results))
	for _, r := range results {
		// The same mark `docker search` uses, so the column reads familiarly.
		official := ""
		if r.Official {
			official = "[OK]"
		}
		rows = append(rows, []string{
			r.Repo.String(),
			format.Count(int64(r.Stars)),
			format.Count(r.Pulls),
			official,
			truncate(strings.Join(strings.Fields(r.Description), " "), descriptionWidth),
		})
	}
	return a.writeTable([]string{"NAME", "STARS", "PULLS", "OFFICIAL", "DESCRIPTION"}, rows)
}

// tagsTable shows the size of the filtered platform when --arch is given,
// otherwise amd64's, like the TUI.
func (a *app) tagsTable(tags []hub.Tag, arch string) int {
	now := a.now()
	rows := make([][]string, 0, len(tags))
	for _, t := range tags {
		rows = append(rows, []string{
			t.Name,
			format.Platforms(t.Platforms, platformsWidth),
			format.Size(format.DisplaySize(t, arch)),
			format.Ago(t.Pushed, now),
			format.ShortDigest(t.Digest),
		})
	}
	return a.writeTable([]string{"TAG", "PLATFORMS", "SIZE", "PUSHED", "DIGEST"}, rows)
}

// writeTable prints the header even when there are no rows, as docker's
// own tables do, so an empty result is visibly empty rather than silent.
func (a *app) writeTable(header []string, rows [][]string) int {
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 3, ' ', 0)
	_, _ = fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, r := range rows {
		_, _ = fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	if err := tw.Flush(); err != nil {
		return a.fail(fmt.Errorf("writing output: %w", err))
	}
	return exitOK
}

func truncate(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	return string(r[:width-1]) + "…"
}
