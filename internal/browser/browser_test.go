package browser

import (
	"context"
	"errors"
	"testing"
)

func TestOpen(t *testing.T) {
	const url = "https://hub.docker.com/_/nginx"
	tests := []struct {
		name      string
		goos      string
		env       map[string]string
		installed bool
		wantCmd   string
		wantErr   error
	}{
		{name: "macOS", goos: "darwin", installed: true, wantCmd: "open"},
		{name: "linux desktop", goos: "linux", env: map[string]string{"WAYLAND_DISPLAY": "wayland-1"}, installed: true, wantCmd: "xdg-open"},
		{name: "linux x11", goos: "linux", env: map[string]string{"DISPLAY": ":0"}, installed: true, wantCmd: "xdg-open"},
		{name: "headless", goos: "linux", installed: true, wantErr: ErrUnavailable},
		{name: "ssh", goos: "darwin", env: map[string]string{"SSH_CONNECTION": "x"}, installed: true, wantErr: ErrUnavailable},
		{name: "no xdg-open", goos: "linux", env: map[string]string{"DISPLAY": ":0"}, wantErr: ErrUnavailable},
		{name: "windows", goos: "windows", installed: true, wantErr: ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ran, arg string
			o := &Opener{
				goos:   tt.goos,
				getenv: func(k string) string { return tt.env[k] },
				look: func(name string) (string, error) {
					if tt.installed {
						return name, nil
					}
					return "", errors.New("not found")
				},
				run: func(_ context.Context, path string, args ...string) error {
					ran, arg = path, args[0]
					return nil
				},
			}
			err := o.Open(context.Background(), url)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) || ran != "" {
					t.Fatalf("err = %v, ran %q; want %v and nothing run", err, ran, tt.wantErr)
				}
				return
			}
			if err != nil || ran != tt.wantCmd || arg != url {
				t.Errorf("ran %q %q, err %v; want %q %q", ran, arg, err, tt.wantCmd, url)
			}
		})
	}
}

func TestRunCommand(t *testing.T) {
	// The URL must arrive as a single argument, untouched by any shell.
	const url = "https://hub.docker.com/_/nginx/tags?name=1.27&page=2"
	if err := runCommand(context.Background(), "/bin/sh", "-c", `test "$1" = "$2"`, "sh", url, url); err != nil {
		t.Errorf("runCommand: %v", err)
	}
	if err := runCommand(context.Background(), "/bin/sh", "-c", "exit 3"); err == nil {
		t.Error("a failing launcher reported success")
	}
}
