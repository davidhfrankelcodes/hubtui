package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		wantUser string
		wantAuth bool
		wantErr  string
	}{
		{name: "anonymous by default", env: nil},
		{name: "both set", env: map[string]string{EnvUsername: "alice", EnvToken: "dckr_pat_x"}, wantUser: "alice", wantAuth: true},
		{name: "whitespace trimmed", env: map[string]string{EnvUsername: " alice\n", EnvToken: "dckr_pat_x\n"}, wantUser: "alice", wantAuth: true},
		{name: "only username", env: map[string]string{EnvUsername: "alice"}, wantErr: "DOCKERHUB_TOKEN is not"},
		{name: "only token", env: map[string]string{EnvToken: "dckr_pat_x"}, wantErr: "DOCKERHUB_USERNAME is not"},
		{name: "blank token counts as unset", env: map[string]string{EnvUsername: "alice", EnvToken: "  "}, wantErr: "set both or neither"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := FromEnv(func(k string) string { return tt.env[k] })
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				if strings.Contains(err.Error(), "dckr_pat") {
					t.Errorf("error leaks the token: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.Username != tt.wantUser || c.Authenticated() != tt.wantAuth {
				t.Errorf("got %+v", c)
			}
			if tt.wantAuth && c.Token.Reveal() != "dckr_pat_x" {
				t.Errorf("token = %q after trimming", c.Token.Reveal())
			}
			if s := fmt.Sprintf("%+v %#v", c, c); strings.Contains(s, "dckr_pat") {
				t.Errorf("formatting leaks the token: %s", s)
			}
		})
	}
}
