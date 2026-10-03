package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

// Scripts read stdout; on any failure it must stay empty so a pipeline
// never consumes half an answer.

func TestSearchJSONError(t *testing.T) {
	code, stdout, stderr := runApp(t, &fakeRegistry{}, "search", "nginx", "--json")
	if code != exitError || stdout != "" || !strings.Contains(stderr, "hubtui: search not configured") {
		t.Errorf("code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

// A tag name Hub should never send must fail the whole command rather than
// print a list with an unusable reference in it.
func TestTagsJSONRejectsInvalidTagName(t *testing.T) {
	reg := &fakeRegistry{tagPages: [][]hub.Tag{{tag("1.27", "amd64"), tag("bad tag", "amd64")}}}
	code, stdout, stderr := runApp(t, reg, "tags", "nginx", "--json")
	if code != exitError || stdout != "" || !strings.Contains(stderr, `invalid tag "bad tag"`) {
		t.Errorf("code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

// A tag that cannot be pinned still prints, with pinned_reference null.
func TestTagsJSONUnpinnable(t *testing.T) {
	legacy := hub.Tag{Name: "1.9", MediaType: "application/vnd.docker.distribution.manifest.v1+prettyjws", Digest: "sha256:" + strings.Repeat("0", 64)}
	noDigest := hub.Tag{Name: "1.8"}
	reg := &fakeRegistry{tagPages: [][]hub.Tag{{legacy, noDigest}}}
	code, stdout, stderr := runApp(t, reg, "tags", "nginx", "--json")
	if code != exitOK {
		t.Fatalf("code %d, stderr %q", code, stderr)
	}
	for _, want := range []string{`"reference": "nginx:1.9"`, `"reference": "nginx:1.8"`, `"digest": null`, `"pushed": null`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %s:\n%s", want, stdout)
		}
	}
	if n := strings.Count(stdout, `"pinned_reference": null`); n != 2 {
		t.Errorf("%d null pinned references, want 2:\n%s", n, stdout)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestWriteJSONFailure(t *testing.T) {
	var errOut bytes.Buffer
	a := &app{stdout: failingWriter{}, stderr: &errOut, registry: &fakeRegistry{search: &hub.SearchPage{}}}
	if code := a.run(context.Background(), []string{"search", "x", "--json"}); code != exitError {
		t.Errorf("code %d, want %d", code, exitError)
	}
	if !strings.Contains(errOut.String(), "writing output: broken pipe") {
		t.Errorf("stderr %q", errOut.String())
	}
}
