package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

// RunTags runs the TUI on the Tags screen for repo until the user quits.
func RunTags(ctx context.Context, reg hub.Registry, repo hub.Repo) error {
	m := NewTagsModel(ctx, reg, repo, time.Now)
	if _, err := tea.NewProgram(m, tea.WithContext(ctx)).Run(); err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}
	return nil
}
