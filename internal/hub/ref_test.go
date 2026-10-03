package hub

import (
	"errors"
	"strings"
	"testing"
)

const (
	nginxIndex = "sha256:756444d493424be61c13714ec55c97a733942d67772fca9d1724fb264f8bde08"
	ociIndex   = "application/vnd.oci.image.index.v1+json"
)

// These are exact-match on purpose: a wrong reference is the worst bug this
// tool can have.
func TestReference(t *testing.T) {
	tests := []struct {
		repo    Repo
		tag     string
		want    string
		wantErr string
	}{
		{repo: Repo{"library", "nginx"}, tag: "1.27", want: "nginx:1.27"},
		{repo: Repo{"library", "nginx"}, tag: "stable-alpine3.24-perl", want: "nginx:stable-alpine3.24-perl"},
		{repo: Repo{"grafana", "grafana"}, tag: "11.0.0", want: "grafana/grafana:11.0.0"},
		{repo: Repo{"bitnami", "postgresql-repmgr"}, tag: "16_debian.12-r3", want: "bitnami/postgresql-repmgr:16_debian.12-r3"},
		{repo: Repo{"library", "hello-world"}, tag: "nanoserver-ltsc2022", want: "hello-world:nanoserver-ltsc2022"},
		{repo: Repo{"library", "nginx"}, tag: "Latest_V2", want: "nginx:Latest_V2"},
		{repo: Repo{"library", "nginx"}, tag: strings.Repeat("a", 128), want: "nginx:" + strings.Repeat("a", 128)},

		{repo: Repo{"library", "nginx"}, tag: "", wantErr: "invalid tag"},
		{repo: Repo{"library", "nginx"}, tag: ".hidden", wantErr: "invalid tag"},
		{repo: Repo{"library", "nginx"}, tag: "-dash", wantErr: "invalid tag"},
		{repo: Repo{"library", "nginx"}, tag: "has space", wantErr: "invalid tag"},
		{repo: Repo{"library", "nginx"}, tag: "a:b", wantErr: "invalid tag"},
		{repo: Repo{"library", "nginx"}, tag: "1.27\n", wantErr: "invalid tag"},
		{repo: Repo{"library", "nginx"}, tag: strings.Repeat("a", 129), wantErr: "invalid tag"},
		{repo: Repo{"Library", "nginx"}, tag: "1", wantErr: "invalid repository"},
		{repo: Repo{"", "nginx"}, tag: "1", wantErr: "invalid repository"},
	}
	for _, tt := range tests {
		t.Run(tt.repo.Namespace+"/"+tt.repo.Name+":"+tt.tag, func(t *testing.T) {
			got, err := Reference(tt.repo, tt.tag)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Reference() = %q, %v; want error containing %q", got, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("Reference() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestPinnedReference(t *testing.T) {
	tests := []struct {
		name     string
		repo     Repo
		tag      Tag
		want     string
		wantErr  string
		noDigest bool
	}{
		{
			name: "official multi-arch uses the index digest",
			repo: Repo{"library", "nginx"},
			tag: Tag{Name: "stable-alpine3.24-perl", Digest: nginxIndex, MediaType: ociIndex, Platforms: []Platform{
				{OS: "linux", Arch: "amd64", Digest: "sha256:604b4e5233f9c948f4d26392354d76c582a4ce19833816374c1307b7fb59b53b"},
			}},
			want: "nginx:stable-alpine3.24-perl@" + nginxIndex,
		},
		{
			name: "namespaced docker manifest list",
			repo: Repo{"grafana", "grafana"},
			tag: Tag{
				Name:      "nightly-slim",
				Digest:    "sha256:5c1a2935a24ddfc328cb387939ae669ad1405338d38596b7ef55bc36ec90f2e8",
				MediaType: "application/vnd.docker.distribution.manifest.list.v2+json",
			},
			want: "grafana/grafana:nightly-slim@sha256:5c1a2935a24ddfc328cb387939ae669ad1405338d38596b7ef55bc36ec90f2e8",
		},
		{
			name: "single-platform manifest",
			repo: Repo{"library", "nginx"},
			tag:  Tag{Name: "1.27", Digest: nginxIndex, MediaType: "application/vnd.docker.distribution.manifest.v2+json"},
			want: "nginx:1.27@" + nginxIndex,
		},
		{
			name:     "no digest",
			repo:     Repo{"library", "hello-seattle"},
			tag:      Tag{Name: "nanoserver1709", MediaType: "application/vnd.docker.distribution.manifest.v2+json"},
			wantErr:  "no digest for hello-seattle:nanoserver1709",
			noDigest: true,
		},
		{
			name: "schema 1 is refused even with a digest",
			repo: Repo{"library", "nginx"},
			tag: Tag{
				Name:      "1.7.7",
				Digest:    "sha256:e2b6a316056d26eb1b51f7a659adbd4fee8b20c72eb6b4a8fadd4f683d83c6f4",
				MediaType: "application/vnd.docker.distribution.manifest.v1+prettyjws",
			},
			wantErr:  "legacy schema-1",
			noDigest: true,
		},
		{
			name:    "uppercase digest",
			repo:    Repo{"library", "nginx"},
			tag:     Tag{Name: "1", Digest: strings.ToUpper(nginxIndex), MediaType: ociIndex},
			wantErr: "unexpected digest",
		},
		{
			name:    "truncated digest",
			repo:    Repo{"library", "nginx"},
			tag:     Tag{Name: "1", Digest: nginxIndex[:40], MediaType: ociIndex},
			wantErr: "unexpected digest",
		},
		{
			name:    "missing algorithm",
			repo:    Repo{"library", "nginx"},
			tag:     Tag{Name: "1", Digest: strings.TrimPrefix(nginxIndex, "sha256:"), MediaType: ociIndex},
			wantErr: "unexpected digest",
		},
		{
			name:    "invalid tag",
			repo:    Repo{"library", "nginx"},
			tag:     Tag{Name: "bad tag", Digest: nginxIndex, MediaType: ociIndex},
			wantErr: "invalid tag",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PinnedReference(tt.repo, tt.tag)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("PinnedReference() = %q, %v; want error containing %q", got, err, tt.wantErr)
				}
				if errors.Is(err, ErrNoDigest) != tt.noDigest {
					t.Errorf("errors.Is(err, ErrNoDigest) = %v, want %v", !tt.noDigest, tt.noDigest)
				}
				if got != "" {
					t.Errorf("returned %q alongside an error", got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("PinnedReference() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}
