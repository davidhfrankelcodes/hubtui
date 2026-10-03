package hub

import (
	"strings"
	"testing"
)

// These run their seeds on every `go test`; `go test -fuzz=FuzzX ./internal/hub`
// explores further.

// Whatever ParseRepo accepts must print back to itself and be usable in a
// reference, or the TUI could open a repository it cannot yank from.
func FuzzParseRepo(f *testing.F) {
	for _, s := range []string{"nginx", "library/nginx", "_/nginx", "docker.io/nginx", "index.docker.io/grafana/grafana", "some_user/my.repo", "nginx:latest", "a/b/c", "ghcr.io/x/y", " ", "NGINX", "a..b", "-x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		r, err := ParseRepo(s)
		if err != nil {
			return
		}
		again, err := ParseRepo(r.String())
		if err != nil || again != r {
			t.Fatalf("ParseRepo(%q) = %+v, but its String %q parses to %+v, %v", s, r, r.String(), again, err)
		}
		if _, err := Reference(r, "latest"); err != nil {
			t.Fatalf("ParseRepo(%q) = %+v, which Reference rejects: %v", s, r, err)
		}
	})
}

// A pinned reference must split back into exactly the repository, tag and
// digest it was built from.
func FuzzPinnedReference(f *testing.F) {
	digest := "sha256:" + strings.Repeat("ab", 32)
	f.Add("library", "nginx", "1.27", digest)
	f.Add("grafana", "grafana", "12.0.0-ubuntu", digest)
	f.Add("library", "nginx", "a@b", digest)
	f.Add("library", "nginx", "latest", "sha256:XYZ")
	f.Add("Library", "nginx", ".hidden", "")
	f.Fuzz(func(t *testing.T, ns, name, tag, dg string) {
		repo := Repo{Namespace: ns, Name: name}
		ref, err := PinnedReference(repo, Tag{Name: tag, Digest: dg})
		if err != nil {
			return
		}
		if strings.ContainsAny(ref, " \t\n\r") || strings.Count(ref, "@") != 1 {
			t.Fatalf("malformed reference %q", ref)
		}
		image, gotDigest, _ := strings.Cut(ref, "@")
		i := strings.LastIndex(image, ":")
		gotRepo, err := ParseRepo(image[:i])
		if err != nil || gotRepo != repo || image[i+1:] != tag || gotDigest != dg {
			t.Fatalf("%q splits to %+v (%v), tag %q, digest %q; built from %+v, %q, %q", ref, gotRepo, err, image[i+1:], gotDigest, repo, tag, dg)
		}
	})
}

func FuzzIsPrerelease(f *testing.F) {
	for _, s := range []string{"latest", "3.15.0rc2", "1.31-alpine", "sha-1a2b3c4", "8.8-m03", "", "a1", "ÄLPHA"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := IsPrerelease(s)
		// Tag names are ASCII; case must not matter there.
		if isASCII(s) && IsPrerelease(strings.ToUpper(s)) != got {
			t.Fatalf("IsPrerelease(%q) = %v but the upper-case form differs", s, got)
		}
	})
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
