package hub

import (
	"strings"
	"testing"
)

func TestParseRepo(t *testing.T) {
	tests := []struct {
		in      string
		want    Repo
		display string
		wantErr string
	}{
		{in: "nginx", want: Repo{"library", "nginx"}, display: "nginx"},
		{in: "  nginx  ", want: Repo{"library", "nginx"}, display: "nginx"},
		{in: "library/nginx", want: Repo{"library", "nginx"}, display: "nginx"},
		{in: "_/nginx", want: Repo{"library", "nginx"}, display: "nginx"},
		{in: "docker.io/nginx", want: Repo{"library", "nginx"}, display: "nginx"},
		{in: "docker.io/library/nginx", want: Repo{"library", "nginx"}, display: "nginx"},
		{in: "index.docker.io/grafana/grafana", want: Repo{"grafana", "grafana"}, display: "grafana/grafana"},
		{in: "grafana/grafana", want: Repo{"grafana", "grafana"}, display: "grafana/grafana"},
		{in: "bitnami/postgresql-repmgr", want: Repo{"bitnami", "postgresql-repmgr"}, display: "bitnami/postgresql-repmgr"},
		{in: "some_user/my.repo", want: Repo{"some_user", "my.repo"}, display: "some_user/my.repo"},

		{in: "", wantErr: "empty"},
		{in: "nginx:latest", wantErr: "without a tag or digest"},
		{in: "grafana/grafana:11.0.0", wantErr: "without a tag or digest"},
		{in: "nginx@sha256:abc", wantErr: "without a tag or digest"},
		{in: "ghcr.io/foo/bar", wantErr: "only Docker Hub"},
		{in: "localhost/foo", wantErr: "only Docker Hub"},
		{in: "localhost:5000/foo", wantErr: "only Docker Hub"},
		{in: "a/b/c", wantErr: "expected [namespace/]name"},
		{in: "Nginx", wantErr: "invalid repository name"},
		{in: "grafana/", wantErr: "invalid repository name"},
		{in: "-bad/name", wantErr: "invalid repository name"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseRepo(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseRepo(%q) error = %v, want containing %q", tt.in, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRepo(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseRepo(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
			if got.String() != tt.display {
				t.Errorf("String() = %q, want %q", got.String(), tt.display)
			}
		})
	}
}

func TestPlatformString(t *testing.T) {
	tests := []struct {
		p    Platform
		want string
	}{
		{Platform{OS: "linux", Arch: "amd64"}, "linux/amd64"},
		{Platform{OS: "linux", Arch: "arm64", Variant: "v8"}, "linux/arm64/v8"},
		{Platform{Arch: "amd64"}, "amd64"},
	}
	for _, tt := range tests {
		if got := tt.p.String(); got != tt.want {
			t.Errorf("%+v.String() = %q, want %q", tt.p, got, tt.want)
		}
	}
}

func TestPlatformMatches(t *testing.T) {
	armV7 := Platform{OS: "linux", Arch: "arm", Variant: "v7"}
	arm64 := Platform{OS: "linux", Arch: "arm64", Variant: "v8"}
	amd64 := Platform{OS: "linux", Arch: "amd64"}
	legacy := Platform{Arch: "amd64"}
	tests := []struct {
		p    Platform
		spec string
		want bool
	}{
		{amd64, "amd64", true},
		{amd64, "linux/amd64", true},
		{amd64, "windows/amd64", false},
		{amd64, "arm64", false},
		{amd64, "", false},
		{arm64, "arm64", true},
		{arm64, "linux/arm64", true},
		{arm64, "arm64/v8", true},
		{arm64, "linux/arm64/v8", true},
		{armV7, "arm", true},
		{armV7, "arm/v7", true},
		{armV7, "arm/v6", false},
		{armV7, "linux/arm/v7", true},
		{legacy, "amd64", true},
		{legacy, "linux/amd64", false},
	}
	for _, tt := range tests {
		if got := tt.p.Matches(tt.spec); got != tt.want {
			t.Errorf("%s.Matches(%q) = %v, want %v", tt.p, tt.spec, got, tt.want)
		}
	}
}
