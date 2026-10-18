# hubtui

Browse Docker Hub from your terminal: search repositories, filter a repository's tags by name and architecture, see per-platform digests and sizes, and copy a pinned image reference without opening a browser.

![hubtui demo: searching for postgres, hiding unstable tags, grouping by major version, filtering one version to Alpine on arm64, opening a tag to see its platforms and aliases, copying its pinned reference, then listing tags as a table from the command line](docs/demo.gif)

## Why

Finding the right tag on hub.docker.com is slow: tags are paginated, filtering is weak, and architectures and digests are a click away each. The Docker CLI cannot list remote tags at all, so the usual workaround is a `curl | jq` shell function. hubtui replaces that function with something you can also script.

## Install

From source (Go 1.27.1 or later):

```sh
go install github.com/davidhfrankelcodes/hubtui/cmd/hubtui@latest
```

Or clone and `make build`, which writes `./bin/hubtui`.

Each [GitHub release](https://github.com/davidhfrankelcodes/hubtui/releases) has archives for Linux and macOS on amd64 and arm64, with `SHA256SUMS`. A multi-arch container image is published alongside them:

```sh
docker run -it --rm docker.io/davidhfrankelcodes/hubtui nginx
```

The binary is static and has no runtime dependencies.

## Usage

```sh
hubtui                    # search Docker Hub
hubtui nginx              # browse the tags of nginx
hubtui grafana/grafana    # any namespace works; docker.io/ prefixes are accepted
```

### Keys

| Key | Action |
| --- | --- |
| `j`/`k`, arrows | Move |
| `g` / `G` | Top / bottom |
| `enter` | Open: a repository's tags, a version group, or a tag's platforms |
| `esc` | Back (on the Tags screen, clears a regex filter, then closes a version group) |
| `/` | Filter tags by regex; on the Search screen, edit the query |
| `s` | Cycle sort: pushed, name, size |
| `a` | Cycle architecture filter |
| `u` | Hide unstable tags: rc, beta, nightly, dev and commit builds |
| `v` | Group tags by version: major, then minor, then off |
| `y` | Yank `image:tag` |
| `Y` | Yank `image:tag@sha256:...` |
| `p` | Yank `docker pull image:tag` |
| `o` | Open the page on hub.docker.com |
| `r` | Refresh, bypassing the cache |
| `?` | Help for the current screen |
| `q` | Quit (`ctrl+c` while typing) |

Name sorting understands versions, so `17.9` comes before `17.10`. The size shown for a tag is the linux/amd64 image, or the filtered architecture's when `a` is active. Sorting and filtering apply to the tags loaded so far; more load as you scroll, and a filter keeps loading until it has enough matches to fill the screen.

The tag detail screen lists the tag's **aliases**: other tags with the same index digest, so you can see that `latest` is currently `1.31`, `mainline` and `trixie`. Only loaded tags are searched. When Docker Hub has more tags than were loaded, the line says how many it looked through.

`u` judges tags by name alone. It hides whole words such as `rc`, `alpha`, `beta`, `nightly`, `dev`, `tip`, `edge` and Debian's `sid`, `unstable`, `testing` and `experimental`, pre-release suffixes like `3.15.0b4` and `8.8-m03`, and commit-hash tags. Date-stamped tags stay, since distributions use them for stable snapshots. It cannot recognize a development codename such as Ubuntu's next release.

`v` collapses a long tag list into one row per version, `17` or `1.31`, with how many tags each has and which was pushed most recently. Pick the level that fits the image: Postgres and Node release by major, nginx and Python by minor. Tags without a version (`latest`, `alpine`, codenames, date stamps) are grouped under `other`. `enter` opens a group as an ordinary tag list, and the other filters apply before grouping, so `u` then `v` shows only stable versions. Since groups are few, the list keeps loading pages until it fills the screen, which usually means every tag: about ten requests for a large image.

### Yanking

`Y` copies the tag's **index digest**, the one that covers every platform, so the reference pulls the right image on any architecture. The status bar shows exactly what was copied. hubtui refuses to pin a tag when Docker Hub reports no digest for it, or when the tag is a legacy schema-1 manifest that current Docker cannot pull; `y` and `p` still work for those.

Copies go out as an OSC 52 escape sequence, which reaches your local clipboard even over SSH, and also through `wl-copy`, `xclip` or `pbcopy` when running locally. Inside tmux, OSC 52 needs `set -g set-clipboard on`.

## Scripting

`search`, `tags` and `resolve` print results and exit without starting the UI. `search` and `tags` print a table by default (`--table`) and JSON with `--json`. Errors go to stderr with a non-zero exit code.

```sh
hubtui search <query> [--table | --json]
hubtui tags <image> [--table | --json] [--arch <arch>] [--limit <n>] [--stable]
hubtui resolve <image[:tag]> [--latest [--major]] [--arch <arch>] [--json]
```

```
$ hubtui tags nginx --limit 2
TAG                  PLATFORMS                              SIZE      PUSHED   DIGEST
stable-trixie-perl   amd64,arm64/v8,arm/v7,386,ppc64le,+3   75.8 MB   4d ago   9e81d98c45ae
stable-perl          amd64,arm64/v8,arm/v7,386,ppc64le,+3   75.8 MB   4d ago   9e81d98c45ae
```

The table is for reading and its layout may change; scripts should use `--json`.

`resolve` prints one tag's reference pinned to its multi-arch digest, ready for a Dockerfile:

```
$ hubtui resolve postgres:17-alpine
postgres:17-alpine@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24
$ hubtui resolve postgres:17.4-alpine --latest
postgres:17.11-alpine@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24
```

`--latest` finds the newest tag in the same version line: the same variant (`alpine` stays `alpine`, never `alpine3.24`), the same number of version parts (`17.4` moves to `17.11`, not `17.11.1`), the same major version, and no betas or release candidates unless you started from one, and then only later ones of the same version (`3.14.0rc1` moves to `3.14.0rc2` or `3.14.2`, never `3.15.0a1`). Tags that cannot be pinned, such as legacy ones without a digest, are skipped. `--major` lets the major version move too (`17-alpine` to `18-alpine`). A tag without a version, such as `latest`, cannot be compared. Given a pinned reference whose tag has since moved, `resolve` prints the new pin and notes the change on stderr.

Tags come newest first. `--limit` defaults to 100; `0` means everything Docker Hub will return. `--arch` accepts `arm64`, `linux/arm64`, `arm/v7` and similar, and keeps the tags that have that platform. `--stable` leaves out the same tags as `u`.

```sh
# Official images matching a query
hubtui search redis --json | jq -r '.[] | select(.official) | .name'

# Most recently pushed Alpine tag of postgres that runs on arm64, pinned.
# Tags come in push order, which is not version order.
hubtui tags postgres --json --arch arm64 --limit 200 \
  | jq -r '[.[] | select(.name | test("^[0-9]+\\.[0-9]+-alpine$"))][0].pinned_reference'

# Every tag that is currently the same image as latest
hubtui tags nginx --json --limit 0 \
  | jq -r '(map(select(.name == "latest"))[0].digest) as $d | [.[] | select(.digest == $d) | .name] | join(" ")'

# Tag names with their platform counts
hubtui tags nginx --json --limit 5 | jq -r '.[] | [.name, (.platforms | length)] | @tsv'
```

Each tag has `name`, `reference`, `pinned_reference` (null when the tag cannot be pinned), `digest`, `media_type`, `pushed`, and `platforms` (each with `os`, `architecture`, `variant`, `digest` and `size`). Search results have `name`, `namespace`, `repository`, `official`, `stars`, `pulls` and `description`. Fields may be added but are never renamed or removed.

## Authentication

Anonymous access works out of the box. Anonymous requests are limited, though:

| | Anonymous | Signed in |
| --- | --- | --- |
| Tags per repository | first 1,000 | all |
| Search results | first 200 | more |
| Requests per minute (`x-ratelimit-limit`) | 180 | 600 |

To sign in, create a personal access token on hub.docker.com (Account settings → Personal access tokens; **Public Repo Read-only** is enough) and set both variables:

```sh
export DOCKERHUB_USERNAME=yourname
export DOCKERHUB_TOKEN=dckr_pat_...
```

hubtui reads nothing else: not `~/.docker/config.json` and not credential helpers. The token is never logged or printed. The status bar shows the requests left in the current minute, such as `api 175/180`. Below 10% it turns yellow and says when the quota refills; at zero it turns red. If Docker Hub rate-limits you, hubtui shows when to try again and makes no requests until then.

## Development

Tools are pinned in `mise.toml`; `mise install` provides Go and golangci-lint.

```sh
make build     # ./bin/hubtui
make test      # go test ./... -race
make lint      # golangci-lint run
make fmt       # gofmt + goimports
make release   # dist/ archives and SHA256SUMS (tagged versions only)
make tag TAG=v0.2.0  # create a release tag; see Releasing
make gh-release VERSION=v0.2.0  # publish dist/ as a GitHub release
make image     # multi-arch container image
make demo      # re-record docs/demo.gif from docs/demo.tape (needs Docker)
```

Tests never touch the network: the API client is tested against recorded responses in `testdata/`. CI ([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) runs the same make targets. See `CLAUDE.md` for the architecture and the conventions the code follows.

## Releasing

Versions follow [Semantic Versioning](https://semver.org/); changes are recorded in [`CHANGELOG.md`](CHANGELOG.md).

1. Move the notes under `## [Unreleased]` in `CHANGELOG.md` to a new `## [X.Y.Z] - YYYY-MM-DD` section, update the links at the bottom, and commit on `main`.
2. Run `make tag TAG=vX.Y.Z`. It checks the tree is clean, the changelog entry exists, and lint and tests pass, then creates an annotated tag carrying that entry. It does not push.
3. Run `git push origin vX.Y.Z`. GitHub Actions runs lint and tests again, publishes the image as `vX.Y.Z` and `latest` (`make image-push`), then builds the release archives and publishes them as a GitHub release with the changelog entry as its notes (`make gh-release`).

Publishing the image needs two repository secrets: `DOCKERHUB_PUSH_USERNAME` and `DOCKERHUB_PUSH_TOKEN`, a Docker Hub access token with Read & Write access. The GitHub release uses the workflow's own token.

Prereleases (`vX.Y.Z-rc.N`, `-beta.N`, `-alpha.N`) go through the same steps but never move `latest` or become the latest GitHub release. `make release` and `make image-push` refuse any other version, so an untagged or dirty build cannot be published.
