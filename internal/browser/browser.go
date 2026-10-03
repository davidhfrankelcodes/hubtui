// Package browser opens URLs in the user's web browser.
package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// ErrUnavailable means no local browser can be opened, e.g. over SSH; the
// caller should show the URL instead.
var ErrUnavailable = errors.New("no browser available")

// timeout covers the launcher only; xdg-open and open return once the
// browser has been asked, they do not wait for it to close.
const timeout = 5 * time.Second

// Opener launches the platform's URL handler.
type Opener struct {
	goos   string
	getenv func(string) string
	look   func(string) (string, error)
	run    func(ctx context.Context, path string, args ...string) error
}

// New returns an Opener using the real environment.
func New() *Opener {
	return &Opener{goos: runtime.GOOS, getenv: os.Getenv, look: exec.LookPath, run: runCommand}
}

// Open opens url in the browser.
func (o *Opener) Open(ctx context.Context, url string) error {
	if o.getenv("SSH_CONNECTION") != "" || o.getenv("SSH_TTY") != "" {
		return fmt.Errorf("remote session: %w", ErrUnavailable)
	}
	var name string
	switch o.goos {
	case "darwin":
		name = "open"
	case "linux", "freebsd", "openbsd", "netbsd":
		if o.getenv("WAYLAND_DISPLAY") == "" && o.getenv("DISPLAY") == "" {
			return fmt.Errorf("no display server: %w", ErrUnavailable)
		}
		name = "xdg-open"
	default:
		return fmt.Errorf("unsupported OS %s: %w", o.goos, ErrUnavailable)
	}
	path, err := o.look(name)
	if err != nil {
		return fmt.Errorf("%s not found: %w", name, ErrUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := o.run(ctx, path, url); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func runCommand(ctx context.Context, path string, args ...string) error {
	// Output stays unattached so a launcher that leaves a child running
	// cannot make Wait block, and its chatter cannot corrupt the TUI.
	if err := exec.CommandContext(ctx, path, args...).Run(); err != nil {
		return fmt.Errorf("running: %w", err)
	}
	return nil
}
