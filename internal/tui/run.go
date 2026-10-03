package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

// RunTags runs the TUI on the Tags screen for repo until the user quits.
// A nil deps.Now defaults to time.Now.
func RunTags(ctx context.Context, deps Deps, repo hub.Repo) error {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	m := NewTagsModel(ctx, deps, repo)
	if _, err := tea.NewProgram(m, tea.WithContext(ctx)).Run(); err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}
	return nil
}
