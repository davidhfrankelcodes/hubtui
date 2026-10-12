BIN     := bin/hubtui
PKG     := ./cmd/hubtui
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# Published versions must look like v1.2.3 or v1.2.3-rc.1. This rejects
# git describe output for untagged or dirty trees, so only a release tag
# reaches Docker Hub. scripts/tag.sh applies the same rule.
RELEASE_VERSION_RE := ^v[0-9]+\.[0-9]+\.[0-9]+(-(alpha|beta|rc)\.[0-9]+)?$$
# Prereleases are published under their own tag but never move latest.
LATEST_TAG = $(if $(findstring -,$(VERSION)),,-t $(IMAGE):latest)

# Container image. PLATFORMS is what `make image` and `make image-push` build.
IMAGE     ?= docker.io/davidhfrankelcodes/hubtui
PLATFORMS ?= linux/amd64,linux/arm64

export CGO_ENABLED := 0

# VHS renders the README demo; its image bundles ttyd, ffmpeg and a browser.
VHS_IMAGE := ghcr.io/charmbracelet/vhs:v0.12.1@sha256:ea49a6a1c529be83153e88321892b5585964418f0b9055e8c1e0d732194234a3

.PHONY: build run test lint fmt clean check-version release tag image image-push demo

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) $(PKG)

run:
	go run $(PKG) $(ARGS)

# -race needs cgo, so re-enable it just for tests; the shipped binary stays static.
test:
	CGO_ENABLED=1 go test ./... -race

lint:
	golangci-lint run

# golangci-lint's formatters run gofmt and goimports, so CI needs no extra tools.
fmt:
	golangci-lint fmt

check-version:
	@echo '$(VERSION)' | grep -Eq '$(RELEASE_VERSION_RE)' || \
		{ echo "VERSION '$(VERSION)' is not a release version like v1.2.3" >&2; exit 1; }

# Archives for linux and darwin on amd64 and arm64, plus SHA256SUMS, in dist/.
release: check-version
	VERSION='$(VERSION)' LDFLAGS='$(LDFLAGS)' ./scripts/release.sh

# Tags HEAD of main as TAG after checking CHANGELOG.md and running lint and
# tests. Does not push; see scripts/tag.sh.
tag:
	TAG='$(TAG)' ./scripts/tag.sh

# Builds the image for every platform without publishing it.
image:
	docker buildx build --platform $(PLATFORMS) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

# Publishes the image as the version, and as latest unless it is a
# prerelease; expects docker login.
image-push: check-version
	docker buildx build --platform $(PLATFORMS) --build-arg VERSION=$(VERSION) \
		-t $(IMAGE):$(VERSION) $(LATEST_TAG) --push .

# Records docs/demo.gif from docs/demo.tape. The binary is built for Linux on
# the host's architecture, which is what Docker runs the VHS container as.
demo:
	GOOS=linux go build -trimpath -ldflags '$(LDFLAGS)' -o bin/demo/hubtui $(PKG)
	docker run --rm -u "$$(id -u):$$(id -g)" -e HOME=/tmp -v "$(CURDIR)":/vhs -w /vhs $(VHS_IMAGE) docs/demo.tape

clean:
	rm -rf bin dist
