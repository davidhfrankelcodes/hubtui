package hub

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// officialNamespace is where Docker Hub keeps official images; users write
// "nginx" but the API only answers to "library/nginx".
const officialNamespace = "library"

// Simplified from the distribution reference grammar: lowercase alphanumerics
// joined by '.', '_' or '-' runs.
var componentRE = regexp.MustCompile(`^[a-z0-9]+(?:[._-]+[a-z0-9]+)*$`)

// Repo identifies a Docker Hub repository.
type Repo struct {
	Namespace string
	Name      string
}

// ParseRepo normalizes user input such as "nginx", "library/nginx",
// "docker.io/grafana/grafana" or "_/nginx" into a Repo.
func ParseRepo(s string) (Repo, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Repo{}, errors.New("repository name is empty")
	}

	parts := strings.Split(s, "/")
	if len(parts) > 1 {
		switch host := parts[0]; {
		case isDockerHubHost(host):
			parts = parts[1:]
		case strings.ContainsAny(host, ".:") || host == "localhost":
			return Repo{}, fmt.Errorf("%q: only Docker Hub repositories are supported", s)
		}
	}

	last := parts[len(parts)-1]
	if strings.ContainsAny(last, ":@") {
		return Repo{}, fmt.Errorf("%q: pass a repository without a tag or digest", s)
	}

	var r Repo
	switch len(parts) {
	case 1:
		r = Repo{Namespace: officialNamespace, Name: parts[0]}
	case 2:
		r = Repo{Namespace: parts[0], Name: parts[1]}
		if r.Namespace == "_" {
			r.Namespace = officialNamespace
		}
	default:
		return Repo{}, fmt.Errorf("%q: expected [namespace/]name", s)
	}

	for _, c := range []string{r.Namespace, r.Name} {
		if !componentRE.MatchString(c) {
			return Repo{}, fmt.Errorf("%q: invalid repository name", s)
		}
	}
	return r, nil
}

func isDockerHubHost(h string) bool {
	switch h {
	case "docker.io", "index.docker.io", "registry-1.docker.io", "hub.docker.com":
		return true
	}
	return false
}

// Official reports whether r is an official image.
func (r Repo) Official() bool { return r.Namespace == officialNamespace }

// String returns the short form people type and docker accepts: "nginx" for
// official images, "namespace/name" otherwise.
func (r Repo) String() string {
	if r.Official() {
		return r.Name
	}
	return r.Namespace + "/" + r.Name
}

// repoFromAPI converts a repo_name from search results. Official images come
// back without a namespace ("nginx").
func repoFromAPI(name string) Repo {
	if ns, n, ok := strings.Cut(name, "/"); ok {
		return Repo{Namespace: ns, Name: n}
	}
	return Repo{Namespace: officialNamespace, Name: name}
}
