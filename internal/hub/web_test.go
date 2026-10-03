package hub

import "testing"

func TestWebURL(t *testing.T) {
	tests := []struct {
		repo Repo
		tag  string
		want string
	}{
		{Repo{"library", "nginx"}, "", "https://hub.docker.com/_/nginx"},
		{Repo{"library", "nginx"}, "1.27-alpine", "https://hub.docker.com/_/nginx/tags?name=1.27-alpine"},
		{Repo{"grafana", "grafana"}, "", "https://hub.docker.com/r/grafana/grafana"},
		{Repo{"grafana", "grafana"}, "11.0.0+security", "https://hub.docker.com/r/grafana/grafana/tags?name=11.0.0%2Bsecurity"},
	}
	for _, tt := range tests {
		if got := WebURL(tt.repo, tt.tag); got != tt.want {
			t.Errorf("WebURL(%v, %q) = %q, want %q", tt.repo, tt.tag, got, tt.want)
		}
	}
}
