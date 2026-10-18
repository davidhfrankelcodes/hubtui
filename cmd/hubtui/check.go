package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/davidhfrankelcodes/hubtui/internal/dockerfile"
	"github.com/davidhfrankelcodes/hubtui/internal/format"
	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

const checkSynopsis = `Usage: hubtui check [file ...] [--major | --minor] [--write] [--table | --json]

Checks the FROM lines of Dockerfiles (./Dockerfile by default; - reads
stdin). Each image is reported as ok, outdated (a newer tag in its version
line, as resolve --latest finds), moved (its tag now points elsewhere),
unpinned (no digest) or single-arch (pinned to one platform's image), with
the pinned reference to use instead. Exits 1 when anything needs updating or
could not be checked, so it can gate CI. --write updates the files in place.

`

// Statuses check adds to hub's for FROM lines it does not look up.
const (
	statusSkipped = "skipped"
	statusError   = "error"
)

// checkJSON is a public interface for scripts, like the shapes in json.go.
type checkJSON struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Stage    string `json:"stage"`
	Platform string `json:"platform"`
	// Image is the reference as written in the file.
	Image  string `json:"image"`
	Status string `json:"status"`
	// Suggested is the pinned reference to use instead; null when the line
	// needs no change or could not be checked.
	Suggested *string `json:"suggested"`
	// Detail says why a line was skipped or failed, or that the answer may
	// be incomplete.
	Detail  string `json:"detail"`
	Partial bool   `json:"partial"`
}

// checkRow is one FROM line and what became of it.
type checkRow struct {
	checkJSON
	from dockerfile.From
}

func (r checkRow) needsChange() bool {
	return r.Suggested != nil || r.Status == statusError
}

type checkFile struct {
	path    string // "-" for stdin
	src     []byte
	rows    []checkRow
	display string
}

func (a *app) runCheck(ctx context.Context, args []string) int {
	fs := a.flagSet("check", checkSynopsis)
	output := addOutputFlags(fs)
	major := fs.Bool("major", false, "suggest newer major versions too (17-alpine to 18-alpine)")
	minor := fs.Bool("minor", false, "keep the minor version too (3.12.1 to 3.12.x, never 3.13)")
	write := fs.Bool("write", false, "update the files in place")
	paths, code, ok := a.parse(fs, args)
	if !ok {
		return code
	}
	asJSON, err := output.json()
	if err != nil {
		return a.usageError(fs, "check: "+err.Error())
	}
	if *major && *minor {
		return a.usageError(fs, "check: --major and --minor cannot be used together")
	}
	if len(paths) == 0 {
		paths = []string{"Dockerfile"}
	}
	for _, p := range paths {
		if p == "-" && *write {
			return a.usageError(fs, "check: --write cannot update stdin")
		}
	}

	var files []*checkFile
	for _, p := range paths {
		f, err := a.readCheckFile(p)
		if err != nil {
			return a.fail(err)
		}
		files = append(files, f)
	}

	c := checker{ctx: ctx, reg: a.registry, line: hub.LineOptions{CrossMajor: *major, SameMinor: *minor}, seen: map[string]checkOutcome{}}
	partial := false
	for _, f := range files {
		stages := map[string]bool{}
		for _, from := range dockerfile.Parse(f.src) {
			row := c.check(from, stages)
			row.File = f.display
			partial = partial || row.Partial
			f.rows = append(f.rows, row)
			if from.Stage != "" {
				stages[strings.ToLower(from.Stage)] = true
			}
		}
	}
	if partial {
		_, _ = fmt.Fprintf(a.stderr, "hubtui: warning: docker hub stopped paging before every tag was seen; a newer one may exist (signing in lifts the limit)\n")
	}

	failed := false
	for _, f := range files {
		if *write {
			if err := a.writeCheckFile(f); err != nil {
				return a.fail(err)
			}
		}
		for _, r := range f.rows {
			if *write {
				failed = failed || r.Status == statusError || r.Suggested != nil && r.from.Start < 0
			} else {
				failed = failed || r.needsChange()
			}
		}
	}

	var printed int
	if asJSON {
		printed = a.checkJSONOut(files)
	} else {
		printed = a.checkTable(files)
	}
	if printed != exitOK {
		return printed
	}
	if failed {
		return exitError
	}
	return exitOK
}

func (a *app) readCheckFile(path string) (*checkFile, error) {
	if path == "-" {
		src, err := io.ReadAll(a.stdin)
		if err != nil {
			return nil, fmt.Errorf("reading stdin: %w", err)
		}
		return &checkFile{path: path, src: src, display: "stdin"}, nil
	}
	// The error already names the file: "open Dockerfile: no such file".
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &checkFile{path: path, src: src, display: path}, nil
}

// writeCheckFile replaces each suggested image in place and leaves every
// other byte alone. The rename makes the update all or nothing.
func (a *app) writeCheckFile(f *checkFile) error {
	var edits []dockerfile.Edit
	for _, r := range f.rows {
		if r.Suggested == nil {
			continue
		}
		if r.from.Start < 0 {
			_, _ = fmt.Fprintf(a.stderr, "hubtui: %s:%d: image is split across lines; update it by hand\n", f.display, r.Line)
			continue
		}
		edits = append(edits, dockerfile.Edit{From: r.from, Image: *r.Suggested})
	}
	if len(edits) == 0 {
		return nil
	}
	out, n := dockerfile.Rewrite(f.src, edits)
	if err := writeFileAtomic(f.path, out); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(a.stderr, "hubtui: updated %d %s in %s\n", n, plural(n, "reference", "references"), f.display)
	return nil
}

func writeFileAtomic(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("updating %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".hubtui-*")
	if err != nil {
		return fmt.Errorf("updating %s: %w", path, err)
	}
	// Removing after a successful rename fails harmlessly.
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("updating %s: %w", path, err)
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("updating %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("updating %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("updating %s: %w", path, err)
	}
	return nil
}

type checkOutcome struct {
	res hub.CheckResult
	err error
}

// checker looks each distinct reference up once, however many FROM lines
// and files repeat it.
type checker struct {
	ctx context.Context
	reg hub.Registry
	// line is the version-line scope; Platform is set per FROM line.
	line hub.LineOptions
	seen map[string]checkOutcome
}

func (c *checker) check(from dockerfile.From, stages map[string]bool) checkRow {
	row := checkRow{from: from, checkJSON: checkJSON{
		Line: from.Line, Stage: from.Stage, Platform: from.Platform, Image: from.Image,
	}}
	skip := func(why string) checkRow {
		row.Status, row.Detail = statusSkipped, why
		return row
	}
	switch {
	case strings.EqualFold(from.Image, "scratch"):
		return skip("scratch is not an image")
	case stages[strings.ToLower(from.Image)]:
		return skip("an earlier build stage")
	case strings.Contains(from.Image, "$"):
		return skip("uses a build arg")
	}
	ref, err := hub.ParseReference(from.Image)
	switch {
	case errors.Is(err, hub.ErrNotDockerHub):
		return skip("not on docker hub")
	case err != nil:
		row.Status, row.Detail = statusError, err.Error()
		return row
	case ref.Tag == "" && ref.Digest != "":
		return skip("pinned by digest only; add the tag to check it")
	case ref.Tag == "":
		ref.Tag = "latest" // what docker pulls
	}

	// A variable platform ($BUILDPLATFORM) is only known at build time.
	var platform string
	if from.Platform != "" && !strings.Contains(from.Platform, "$") {
		platform = strings.ToLower(from.Platform)
	}
	key := strings.Join([]string{ref.Repo.String(), ref.Tag, ref.Digest, platform}, " ")
	out, ok := c.seen[key]
	if !ok {
		opts := c.line
		opts.Platform = platform
		out.res, out.err = hub.CheckReference(c.ctx, c.reg, ref, opts)
		c.seen[key] = out
	}
	if out.err != nil {
		row.Status, row.Detail = statusError, out.err.Error()
		return row
	}
	row.Status, row.Partial = string(out.res.Status), out.res.Partial
	if row.Partial {
		row.Detail = "a newer tag may exist past docker hub's paging limit"
	}
	if out.res.Status == hub.CheckOK {
		return row
	}
	pinned, err := hub.PinnedReference(ref.Repo, out.res.Suggested)
	if err != nil {
		row.Status, row.Detail = statusError, err.Error()
		return row
	}
	// Keep the name as the file writes it (docker.io/library/postgres
	// stays that way); only the tag and digest change.
	suggested := imageName(from.Image) + strings.TrimPrefix(pinned, ref.Repo.String())
	row.Suggested = &suggested
	return row
}

// imageName is image without its tag and digest.
func imageName(image string) string {
	name, _, _ := strings.Cut(image, "@")
	if i := strings.LastIndexByte(name, ':'); i > strings.LastIndexByte(name, '/') {
		name = name[:i]
	}
	return name
}

func (a *app) checkJSONOut(files []*checkFile) int {
	out := []checkJSON{}
	for _, f := range files {
		for _, r := range f.rows {
			out = append(out, r.checkJSON)
		}
	}
	return a.writeJSON(out)
}

func (a *app) checkTable(files []*checkFile) int {
	var rows [][]string
	for _, f := range files {
		for _, r := range f.rows {
			update := r.Detail
			if r.Suggested != nil {
				update = shortenDigest(*r.Suggested)
				if r.Detail != "" {
					update += " (" + r.Detail + ")"
				}
			}
			rows = append(rows, []string{
				r.File + ":" + strconv.Itoa(r.Line),
				shortenDigest(r.Image),
				r.Status,
				update,
			})
		}
	}
	return a.writeTable([]string{"LOCATION", "IMAGE", "STATUS", "UPDATE"}, rows)
}

// shortenDigest keeps a pinned reference readable in a table column; the
// JSON and --write use the full digest.
func shortenDigest(ref string) string {
	name, digest, ok := strings.Cut(ref, "@")
	if !ok {
		return ref
	}
	return name + "@sha256:" + format.ShortDigest(digest) + "…"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
