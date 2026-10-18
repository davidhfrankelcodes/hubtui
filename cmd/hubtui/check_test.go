package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// checkDockerfile has one FROM per status; resolveFixture serves its tags.
// Line numbers matter: the tests below name them.
func checkDockerfile() string {
	return strings.Join([]string{
		"# syntax=docker/dockerfile:1",                                 // 1
		"FROM postgres:17.4-alpine AS build",                           // 2 outdated
		"FROM docker.io/library/postgres:17.11-alpine@" + digestFor(3), // 3 ok
		"FROM --platform=linux/arm64 postgres:17.4-alpine",             // 4 outdated, arm64
		"FROM build",           // 5 skipped
		"FROM scratch",         // 6 skipped
		"FROM ghcr.io/o/app:1", // 7 skipped
		"FROM golang:${GO}",    // 8 skipped
		"FROM postgres:17-alpine@" + digestFor(99) + " AS moved", // 9 moved
		"FROM postgres",                                       // 10 unpinned
		"FROM postgres:9.6",                                   // 11 error
		"FROM postgres@" + digestFor(5),                       // 12 skipped
		"FROM --platform=$BUILDPLATFORM postgres:17.4-alpine", // 13 outdated, same as 2
		"",
	}, "\n")
}

type checkWant struct {
	line      int
	status    string
	suggested string // "" for null
}

func checkWants() []checkWant {
	return []checkWant{
		{2, "outdated", "postgres:17.11-alpine@" + digestFor(3)},
		{3, "ok", ""},
		{4, "outdated", "postgres:17.10-alpine@" + digestFor(4)},
		{5, "skipped", ""},
		{6, "skipped", ""},
		{7, "skipped", ""},
		{8, "skipped", ""},
		{9, "moved", "postgres:17-alpine@" + digestFor(5)},
		{10, "unpinned", "postgres:latest@" + digestFor(0)},
		{11, "error", ""},
		{12, "skipped", ""},
		{13, "outdated", "postgres:17.11-alpine@" + digestFor(3)},
	}
}

func writeDockerfile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Dockerfile")
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

func decodeCheck(t *testing.T, stdout string) []checkJSON {
	t.Helper()
	var rows []checkJSON
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	return rows
}

// Suggestions land in Dockerfiles, so they are compared exactly.
func TestCheckJSON(t *testing.T) {
	path := writeDockerfile(t, checkDockerfile())
	code, stdout, stderr := runApp(t, resolveFixture(), "check", path, "--json")
	if code != exitError {
		t.Errorf("exit %d, want 1 since lines need updating; stderr %s", code, stderr)
	}
	rows := decodeCheck(t, stdout)
	if len(rows) != len(checkWants()) {
		t.Fatalf("got %d rows, want %d:\n%s", len(rows), len(checkWants()), stdout)
	}
	for i, w := range checkWants() {
		r := rows[i]
		got := ""
		if r.Suggested != nil {
			got = *r.Suggested
		}
		if r.Line != w.line || r.Status != w.status || got != w.suggested || r.File != path {
			t.Errorf("row %d = %s:%d %s %q (%s), want line %d %s %q", i, r.File, r.Line, r.Status, got, r.Detail, w.line, w.status, w.suggested)
		}
	}
	// Spot-check the fields scripts select on.
	if rows[0].Stage != "build" || rows[2].Platform != "linux/arm64" || rows[1].Image != "docker.io/library/postgres:17.11-alpine@"+digestFor(3) {
		t.Errorf("rows = %+v", rows[:3])
	}
	if !strings.Contains(rows[9].Detail, "legacy") || rows[3].Detail != "an earlier build stage" {
		t.Errorf("details = %q, %q", rows[9].Detail, rows[3].Detail)
	}
}

func TestCheckWrite(t *testing.T) {
	path := writeDockerfile(t, checkDockerfile())
	code, stdout, stderr := runApp(t, resolveFixture(), "check", path, "--write")
	// Line 11 cannot be fixed, so the run still fails.
	if code != exitError || !strings.Contains(stderr, "updated 5 references in "+path) {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	if !strings.HasPrefix(stdout, "LOCATION") {
		t.Errorf("no table: %s", stdout)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"# syntax=docker/dockerfile:1",
		"FROM postgres:17.11-alpine@" + digestFor(3) + " AS build",
		"FROM docker.io/library/postgres:17.11-alpine@" + digestFor(3),
		"FROM --platform=linux/arm64 postgres:17.10-alpine@" + digestFor(4),
		"FROM build",
		"FROM scratch",
		"FROM ghcr.io/o/app:1",
		"FROM golang:${GO}",
		"FROM postgres:17-alpine@" + digestFor(5) + " AS moved",
		"FROM postgres:latest@" + digestFor(0),
		"FROM postgres:9.6",
		"FROM postgres@" + digestFor(5),
		"FROM --platform=$BUILDPLATFORM postgres:17.11-alpine@" + digestFor(3),
		"",
	}, "\n")
	if string(got) != want {
		t.Errorf("file after --write:\n%s\nwant:\n%s", got, want)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640 kept", info.Mode().Perm())
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("temporary file left behind: %v", entries)
	}

	// Run again: everything that could be fixed is now ok.
	code, stdout, _ = runApp(t, resolveFixture(), "check", path, "--json")
	for _, r := range decodeCheck(t, stdout) {
		if r.Status != "ok" && r.Status != "skipped" && r.Line != 11 {
			t.Errorf("line %d still %s after --write", r.Line, r.Status)
		}
	}
	if code != exitError {
		t.Errorf("exit %d; line 11 still fails", code)
	}
}

func TestCheckExitCodes(t *testing.T) {
	current := "FROM postgres:17.11-alpine@" + digestFor(3) + "\nFROM scratch\n"
	tests := []struct {
		name    string
		content string
		args    []string
		want    int
	}{
		{name: "all current", content: current, want: exitOK},
		{name: "write with nothing to do", content: current, args: []string{"--write"}, want: exitOK},
		{name: "outdated", content: "FROM postgres:17.4-alpine\n", want: exitError},
		{name: "write fixes it", content: "FROM postgres:17.4-alpine\n", args: []string{"--write"}, want: exitOK},
		{name: "only skipped lines", content: "FROM scratch\nFROM ghcr.io/o/app:1\n", want: exitOK},
		{name: "no FROM at all", content: "# empty\n", want: exitOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeDockerfile(t, tt.content)
			code, _, stderr := runApp(t, resolveFixture(), append([]string{"check", path}, tt.args...)...)
			if code != tt.want {
				t.Errorf("exit %d, want %d; stderr %s", code, tt.want, stderr)
			}
		})
	}
}

func TestCheckTable(t *testing.T) {
	path := writeDockerfile(t, "FROM postgres:17.4-alpine\nFROM scratch\n")
	_, stdout, _ := runApp(t, resolveFixture(), "check", path)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "LOCATION") {
		t.Fatalf("table:\n%s", stdout)
	}
	// Digests are shortened for reading; the JSON has them in full.
	short := "postgres:17.11-alpine@sha256:" + strings.TrimPrefix(digestFor(3), "sha256:")[:12] + "…"
	if f := strings.Fields(lines[1]); len(f) != 4 || f[0] != path+":1" || f[2] != "outdated" || f[3] != short {
		t.Errorf("row = %q", lines[1])
	}
}

func TestCheckStdin(t *testing.T) {
	code, stdout, _ := runAppWithStdin(t, resolveFixture(), "FROM postgres:17.4-alpine\n", "check", "-", "--json")
	rows := decodeCheck(t, stdout)
	if code != exitError || len(rows) != 1 || rows[0].File != "stdin" || rows[0].Status != "outdated" {
		t.Errorf("exit %d, rows %+v", code, rows)
	}
}

// Line 13 repeats line 2's image; it must not cost another request.
func TestCheckLooksUpEachImageOnce(t *testing.T) {
	path := writeDockerfile(t, "FROM postgres:17.4-alpine\nFROM postgres:17.4-alpine AS b\nFROM --platform=$BUILDPLATFORM postgres:17.4-alpine\n")
	reg := resolveFixture()
	if code, _, stderr := runApp(t, reg, "check", path); code != exitError {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if len(reg.calls) != 1 {
		t.Errorf("%d tag listings, want 1", len(reg.calls))
	}
}

func TestCheckSingleArch(t *testing.T) {
	reg := resolveFixture()
	tags := reg.tagPages[0]
	tags[3].Platforms[0].Digest = digestFor(300) // 17.11-alpine's amd64 image
	path := writeDockerfile(t, "FROM postgres:17.11-alpine@"+digestFor(300)+"\n")
	_, stdout, _ := runApp(t, reg, "check", path, "--json")
	rows := decodeCheck(t, stdout)
	if len(rows) != 1 || rows[0].Status != "single-arch" || rows[0].Suggested == nil || *rows[0].Suggested != "postgres:17.11-alpine@"+digestFor(3) {
		t.Errorf("rows = %+v", rows)
	}
}

func TestCheckMinor(t *testing.T) {
	path := writeDockerfile(t, "FROM postgres:17.4.1-alpine\n")
	_, stdout, _ := runApp(t, resolveFixture(), "check", path, "--minor", "--json")
	rows := decodeCheck(t, stdout)
	if len(rows) != 1 || rows[0].Suggested == nil || *rows[0].Suggested != "postgres:17.4.3-alpine@"+digestFor(21) {
		t.Errorf("rows = %+v", rows)
	}
}

func TestCheckMajor(t *testing.T) {
	path := writeDockerfile(t, "FROM postgres:17.4-alpine\n")
	_, stdout, _ := runApp(t, resolveFixture(), "check", path, "--major", "--json")
	rows := decodeCheck(t, stdout)
	if len(rows) != 1 || rows[0].Suggested == nil || *rows[0].Suggested != "postgres:18.1-alpine@"+digestFor(1) {
		t.Errorf("rows = %+v", rows)
	}
}

func TestCheckErrors(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{name: "missing file", args: []string{filepath.Join(dir, "nope")}, wantCode: exitError, wantStderr: "no such file"},
		{name: "write to stdin", args: []string{"-", "--write"}, wantCode: exitUsage, wantStderr: "--write cannot update stdin"},
		{name: "table and json", args: []string{"--table", "--json", "-"}, wantCode: exitUsage, wantStderr: "cannot be used together"},
		{name: "major and minor", args: []string{"--major", "--minor", "-"}, wantCode: exitUsage, wantStderr: "--major and --minor cannot be used together"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runApp(t, resolveFixture(), append([]string{"check"}, tt.args...)...)
			if code != tt.wantCode || stdout != "" || !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("code %d, stdout %q, stderr %q (want %q)", code, stdout, stderr, tt.wantStderr)
			}
		})
	}
}

// With no file named, check reads ./Dockerfile, as docker build does.
func TestCheckDefaultsToDockerfile(t *testing.T) {
	dir := filepath.Dir(writeDockerfile(t, "FROM postgres:17.4-alpine\n"))
	t.Chdir(dir)
	_, stdout, stderr := runApp(t, resolveFixture(), "check", "--json")
	if rows := decodeCheck(t, stdout); len(rows) != 1 || rows[0].File != "Dockerfile" {
		t.Errorf("rows %+v, stderr %s", rows, stderr)
	}
}
