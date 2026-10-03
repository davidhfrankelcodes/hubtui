package clip

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ErrUnavailable means there is no native clipboard to use here; the caller
// still has OSC 52.
var ErrUnavailable = errors.New("no native clipboard available")

// timeout bounds a clipboard tool that hangs, e.g. xclip with no X server.
const timeout = 3 * time.Second

// Native copies text with the platform's clipboard command.
//
// The TUI always sends OSC 52 too, because it reaches the user's local
// clipboard even over SSH, but there is no way to tell whether the terminal
// honored it (tmux ignores it by default). Native is the fallback that works
// locally regardless.
type Native struct {
	goos   string
	getenv func(string) string
	look   func(string) (string, error)
	run    func(ctx context.Context, path string, args []string, stdin string) error
}

// NewNative returns a Native using the real environment.
func NewNative() *Native {
	return &Native{goos: runtime.GOOS, getenv: os.Getenv, look: exec.LookPath, run: runCommand}
}

// Copy puts text on the native clipboard.
func (n *Native) Copy(ctx context.Context, text string) error {
	name, args, err := n.command()
	if err != nil {
		return err
	}
	path, err := n.look(name)
	if err != nil {
		return fmt.Errorf("%s not found: %w", name, ErrUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := n.run(ctx, path, args, text); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func (n *Native) command() (name string, args []string, err error) {
	// Over SSH a native tool would fill the remote machine's clipboard,
	// which is never what the user wants; OSC 52 covers this case.
	if n.getenv("SSH_CONNECTION") != "" || n.getenv("SSH_TTY") != "" {
		return "", nil, fmt.Errorf("remote session: %w", ErrUnavailable)
	}
	switch n.goos {
	case "darwin":
		return "pbcopy", nil, nil
	case "linux", "freebsd", "openbsd", "netbsd":
		switch {
		case n.getenv("WAYLAND_DISPLAY") != "":
			return "wl-copy", nil, nil
		case n.getenv("DISPLAY") != "":
			return "xclip", []string{"-selection", "clipboard"}, nil
		}
		return "", nil, fmt.Errorf("no display server: %w", ErrUnavailable)
	}
	return "", nil, fmt.Errorf("unsupported OS %s: %w", n.goos, ErrUnavailable)
}

func runCommand(ctx context.Context, path string, args []string, stdin string) error {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin = strings.NewReader(stdin)
	// Leave stdout and stderr unset: wl-copy and xclip fork a child that
	// keeps serving the selection, and inheriting a pipe would make Wait
	// block until that child exits.
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running: %w", err)
	}
	return nil
}
