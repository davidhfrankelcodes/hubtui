# hubtui

Browse Docker Hub from your terminal: search repositories, filter a repository's tags by name and architecture, see per-platform digests and sizes, and copy a pinned image reference without opening a browser.

![hubtui demo: searching for postgres, filtering tags by regex and architecture, opening a tag and copying its pinned reference](docs/demo.gif)

## Why

Finding the right tag on hub.docker.com is slow: tags are paginated, filtering is weak, and architectures and digests are a click away each. The Docker CLI cannot list remote tags at all, so the usual workaround is a `curl | jq` shell function. hubtui replaces that function with something you can also script.

## Install

From source (Go 1.27.1 or later):

```sh
go install github.com/davidhfrankelcodes/hubtui/cmd/hubtui@latest
```

Or clone and `make build`, which writes `./bin/hubtui`.

Release archives for Linux and macOS on amd64 and arm64, with `SHA256SUMS`, are built by `make release`. A multi-arch container image is built by `make image`:

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
| `enter` | Open: a repository's tags, or a tag's platforms |
| `esc` | Back (on the Tags screen, clears a regex filter first) |
| `/` | Filter tags by regex; on the Search screen, edit the query |
| `s` | Cycle sort: pushed, name, size |
| `a` | Cycle architecture filter |
| `u` | Hide unstable tags: rc, beta, nightly, dev and commit builds |
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

### Yanking

`Y` copies the tag's **index digest**, the one that covers every platform, so the reference pulls the right image on any architecture. The status bar shows exactly what was copied. hubtui refuses to pin a tag when Docker Hub reports no digest for it, or when the tag is a legacy schema-1 manifest that current Docker cannot pull; `y` and `p` still work for those.

Copies go out as an OSC 52 escape sequence, which reaches your local clipboard even over SSH, and also through `wl-copy`, `xclip` or `pbcopy` when running locally. Inside tmux, OSC 52 needs `set -g set-clipboard on`.

## Scripting

`search` and `tags` print JSON and never start the UI. Errors go to stderr with a non-zero exit code.

```sh
hubtui search <query> --json
hubtui tags <image> --json [--arch <arch>] [--limit <n>] [--stable]
```

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
make release   # dist/ archives and SHA256SUMS
make image     # multi-arch container image
make demo      # re-record docs/demo.gif from docs/demo.tape (needs Docker)
```

Tests never touch the network: the API client is tested against recorded responses in `testdata/`. CI (`Jenkinsfile`) runs the same make targets. See `CLAUDE.md` for the architecture and the conventions the code follows.
