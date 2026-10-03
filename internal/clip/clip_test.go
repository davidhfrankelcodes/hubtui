package clip

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNativeCommand(t *testing.T) {
	tests := []struct {
		name      string
		goos      string
		env       map[string]string
		installed []string
		wantCmd   string
		wantErr   error
	}{
		{name: "macOS", goos: "darwin", installed: []string{"pbcopy"}, wantCmd: "pbcopy"},
		{name: "wayland", goos: "linux", env: map[string]string{"WAYLAND_DISPLAY": "wayland-1", "DISPLAY": ":0"}, installed: []string{"wl-copy", "xclip"}, wantCmd: "wl-copy"},
		{name: "x11", goos: "linux", env: map[string]string{"DISPLAY": ":0"}, installed: []string{"xclip"}, wantCmd: "xclip -selection clipboard"},
		{name: "headless linux", goos: "linux", wantErr: ErrUnavailable},
		{name: "tool missing", goos: "linux", env: map[string]string{"WAYLAND_DISPLAY": "wayland-1"}, wantErr: ErrUnavailable},
		{name: "ssh session", goos: "linux", env: map[string]string{"SSH_CONNECTION": "1.2.3.4 22 5.6.7.8 22", "DISPLAY": ":0"}, installed: []string{"xclip"}, wantErr: ErrUnavailable},
		{name: "ssh tty on mac", goos: "darwin", env: map[string]string{"SSH_TTY": "/dev/ttys001"}, installed: []string{"pbcopy"}, wantErr: ErrUnavailable},
		{name: "windows", goos: "windows", wantErr: ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotCmd, gotStdin string
			n := &Native{
				goos:   tt.goos,
				getenv: func(k string) string { return tt.env[k] },
				look: func(name string) (string, error) {
					for _, i := range tt.installed {
						if i == name {
							return "/usr/bin/" + name, nil
						}
					}
					return "", errors.New("not found")
				},
				run: func(_ context.Context, path string, args []string, stdin string) error {
					gotCmd = strings.Join(append([]string{strings.TrimPrefix(path, "/usr/bin/")}, args...), " ")
					gotStdin = stdin
					return nil
				},
			}
			err := n.Copy(context.Background(), "nginx:1.27")
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				if gotCmd != "" {
					t.Errorf("ran %q despite the error", gotCmd)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotCmd != tt.wantCmd {
				t.Errorf("ran %q, want %q", gotCmd, tt.wantCmd)
			}
			if gotStdin != "nginx:1.27" {
				t.Errorf("stdin = %q, want the exact text with no trailing newline", gotStdin)
			}
		})
	}
}

func TestNativeCommandFailure(t *testing.T) {
	n := &Native{
		goos:   "darwin",
		getenv: func(string) string { return "" },
		look:   func(name string) (string, error) { return "/usr/bin/" + name, nil },
		run:    func(context.Context, string, []string, string) error { return errors.New("exit status 1") },
	}
	err := n.Copy(context.Background(), "x")
	if err == nil || errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "pbcopy: exit status 1") {
		t.Errorf("error = %v, want a pbcopy failure distinct from ErrUnavailable", err)
	}
}

func TestRunCommandPassesStdin(t *testing.T) {
	// cat proves the text arrives on stdin byte-for-byte; it exits once
	// stdin closes, like the real tools' foreground process.
	if err := runCommand(context.Background(), "/bin/sh", []string{"-c", `test "$(cat)" = "nginx:1.27@sha256:abc"`}, "nginx:1.27@sha256:abc"); err != nil {
		t.Errorf("runCommand: %v", err)
	}
}
