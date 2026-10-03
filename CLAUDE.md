# hubtui

A terminal UI for browsing Docker Hub: search repositories, browse and filter tags, and copy a pinned image reference without opening a browser.

## Why this exists

Finding the right tag on hub.docker.com is slow (paginated lists, weak filtering, architectures and digests hidden behind clicks), and the Docker CLI cannot list remote tags. The usual workaround is a curl + jq shell function. This tool replaces that.

## Scope

In scope for v1:
- Search repositories
- Browse a repository's tags with arch, size, pushed date, and digest
- Filter, sort, and yank image references to the clipboard
- A non-interactive mode that prints JSON for piping to `jq`

Out of scope for v1 (do not build, do not stub):
- Pushing, deleting, or any write operation against the Hub
- Other registries (GHCR, Quay, ECR). Keep the client behind an interface so they can be added later, but implement Docker Hub only.
- Pulling or running images
- Reading `~/.docker/config.json` or credential helpers

## Stack

- Go (current stable). Single static binary, `CGO_ENABLED=0`.
- Bubble Tea + Bubbles + Lip Gloss for the TUI. Use the current stable major version; check the import path before adding the dependency rather than assuming it.
- Standard library `net/http` for the API client. No third-party HTTP or Docker SDK dependencies.
- `golangci-lint` for linting.

Keep dependencies minimal. Ask before adding any dependency not listed here.

## Commands

```
make build     # builds ./bin/hubtui
make run       # go run ./cmd/hubtui
make test      # go test ./... -race
make lint      # golangci-lint run
make fmt       # gofmt + goimports
```

Run `make lint test` before declaring any task done. CI calls these same make targets and nothing else.

## Layout

```
cmd/hubtui/        main.go: flag parsing, wiring only
internal/hub/      Docker Hub API client and types. No TUI imports.
internal/tui/      Bubble Tea models, one file per screen
internal/clip/     Clipboard (OSC 52 first, native fallback)
internal/config/   Env and flag resolution
testdata/          Recorded API responses used by tests
```

## Architecture rules

- `internal/hub` knows nothing about the TUI. It exposes a `Registry` interface and returns typed structs.
- All network calls run inside a `tea.Cmd`. Never block in `Update` or `View`.
- Every request takes a `context.Context`. Cancel in-flight requests when the user navigates away or types a new search.
- Stale responses must be discarded: tag each request with an ID and ignore results that do not match the current one.
- Cache responses in memory for the session with a short TTL; `r` bypasses the cache.
- Pagination is lazy: fetch the next page as the cursor nears the bottom, not all pages up front. Popular images have thousands of tags.

## Docker Hub API notes

Verify each endpoint against the live API with `curl` before writing the client for it. Record the response into `testdata/` and build the types from what actually comes back, not from memory.

- Search: `GET https://hub.docker.com/v2/search/repositories/?query=<q>&page_size=<n>`
- Tags: `GET https://hub.docker.com/v2/namespaces/<ns>/repositories/<repo>/tags?page_size=<n>&ordering=last_updated`
  - `ordering` is inverted from what the names suggest: `last_updated` is newest first, `-last_updated` oldest first; `-name` is ascending. Verified live 2026-10-02.
- `page_size` is silently capped at 100 on both endpoints.
- Anonymous paging is capped: tags stop after the first 1000, search after the first 200. Past that Hub returns 403 with a "pagination ... too large" message (and `next` still points past it).
- Some legacy (schema v1) tags have `digest: null`. Indexes also list attestation manifests as `unknown/unknown` platforms; they are not runnable and must be hidden.
- Official images live in the `library` namespace. Accept `nginx` and normalize to `library/nginx` for requests; display it as `nginx`.
- A tag has a top-level digest (the multi-arch index) and a per-platform list of images, each with its own digest and size. Confirm which is which against `docker buildx imagetools inspect <image>:<tag>` before relying on it. The yanked digest must be the index digest, so the reference works on any architecture.
- Anonymous access is the default. If `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN` are both set, authenticate with them. Never log, print, or write the token anywhere.
- Handle HTTP 429: respect `Retry-After`, show a status-bar message, do not retry in a tight loop.

## Screens

1. **Search.** Text input plus a results table: name, stars, pulls, official badge, truncated description.
2. **Tags.** Table for one repository: tag, architectures, compressed size, pushed (relative time), short digest.
3. **Tag detail.** One row per platform: os/arch/variant, digest, size.

`hubtui <image>` skips search and opens the Tags screen directly (`hubtui nginx`, `hubtui grafana/grafana`).

## Keys

| Key | Action |
| --- | --- |
| `j`/`k`, arrows | Move |
| `g` / `G` | Top / bottom |
| `enter` | Open |
| `esc` | Back |
| `/` | Filter current list (regex on the Tags screen) |
| `s` | Cycle sort: pushed, name, size |
| `a` | Cycle architecture filter |
| `y` | Yank `image:tag` |
| `Y` | Yank `image:tag@sha256:...` |
| `p` | Yank `docker pull image:tag` |
| `o` | Open the page in a browser |
| `r` | Refresh, bypassing cache |
| `?` | Help |
| `q` | Quit |

Yanking shows a one-line confirmation in the status bar with exactly what was copied.

## Non-interactive mode

```
hubtui search <query> --json
hubtui tags <image> --json [--arch <arch>] [--limit <n>]
```

Prints JSON to stdout and exits. No TUI, no color, errors to stderr with a non-zero exit code. If stdout is not a TTY and no subcommand is given, print usage instead of starting the TUI.

## Conventions

- Errors: wrap with `fmt.Errorf("...: %w", err)`. No panics outside `main`.
- No global state. Pass dependencies explicitly.
- Network errors appear in the status bar and never crash the program.
- Respect `NO_COLOR`. Layout must hold at 80x24 and reflow on resize.
- Use the terminal's ANSI palette (semantic colors), not hardcoded hex values, so the UI follows the user's theme.
- Any shell script in this repo prints explicit numbered steps (`[1/4] Building...`) and uses functions rather than aliases.
- Comments explain why, not what.

## Testing

- No live network calls in tests. Serve `testdata/` fixtures from `httptest.Server`.
- Table-driven tests for the client: name normalization, pagination, 429 handling, malformed responses.
- Test TUI models by sending messages to `Update` and asserting on the resulting model state. Do not snapshot rendered output.
- The yank strings (`y`, `Y`, `p`) need exact-match tests; a wrong reference is the worst bug this tool can have.

## Build order

Work through these in order. Stop after each one, run `make lint test`, and summarize what was built before continuing.

1. Repo scaffold: `go.mod`, Makefile, lint config, empty package layout, `--version`.
2. `internal/hub`: search and tags, pagination, fixtures, tests.
3. Non-interactive mode (`search`, `tags`, `--json`). This proves the client before any UI exists.
4. Tags screen, launched via `hubtui <image>`: table, lazy pagination, filter, sort.
5. Yank keys and `internal/clip`.
6. Search screen and navigation between screens.
7. Tag detail screen, arch filter, help overlay.
8. Auth via environment variables and 429 handling.
9. Packaging: release binaries for linux/darwin on amd64/arm64, a multi-arch container image, and a Jenkinsfile that runs the make targets.
10. README with a demo recording.
