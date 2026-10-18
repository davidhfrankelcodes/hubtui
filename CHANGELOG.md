# Changelog

All notable changes to hubtui are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). Until 1.0.0, a
minor version may change key bindings, flags or the JSON output.

## [Unreleased]

### Added

- `hubtui resolve <image[:tag]>` prints a tag's reference pinned to its
  multi-arch digest. `--latest` picks the newest tag of the same variant and
  version precision within the major version (`postgres:17.4-alpine` to
  `17.11-alpine`); `--major` allows a newer major. `--arch` and `--json` work
  as on `tags`. Re-resolving a pinned reference notes when its tag has moved.

## [0.2.0] - 2026-10-12

### Changed

- `hubtui search` and `hubtui tags` print a table by default instead of
  failing without `--json`. `--json` prints JSON as before; `--table` asks
  for the table explicitly.

### Added

- Releases are published on GitHub with the archives, `SHA256SUMS` and the
  changelog entry as notes.

## [0.1.1] - 2026-10-12

### Fixed

- Refreshing with `r` no longer moves the cursor onto a different tag when
  tags were pushed since the last load.

### Changed

- Prerelease container images (`vX.Y.Z-rc.N` and similar) are published
  under their own tag only and no longer update `latest`.

## [0.1.0] - 2026-10-03

First release.

### Added

- Search screen: repositories with stars, pulls, an official badge and a
  description.
- Tags screen, opened from search or directly with `hubtui <image>`:
  architectures, compressed size, push time and digest, loaded lazily page by
  page.
- Regex filter (`/`), sort by push date, name or size (`s`), architecture
  filter (`a`), stable-tags filter (`u`) and version grouping by major or
  minor (`v`).
- Tag detail screen with one row per platform and the tag's aliases.
- Yank `image:tag` (`y`), `image:tag@sha256:...` pinned to the index digest
  (`Y`) and `docker pull image:tag` (`p`), over OSC 52 with a native
  clipboard fallback.
- Open the repository or tag page in a browser (`o`), refresh bypassing the
  cache (`r`), and a help overlay (`?`).
- Non-interactive `hubtui search <query> --json` and
  `hubtui tags <image> --json` with `--arch`, `--limit` and `--stable`.
- Optional sign-in with `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN`, which
  lifts the anonymous paging caps and raises the rate limit.
- Request quota in the status bar, and `Retry-After` handling for HTTP 429.
- Release archives for Linux and macOS on amd64 and arm64, and a multi-arch
  container image.

[Unreleased]: https://github.com/davidhfrankelcodes/hubtui/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/davidhfrankelcodes/hubtui/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/davidhfrankelcodes/hubtui/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/davidhfrankelcodes/hubtui/releases/tag/v0.1.0
