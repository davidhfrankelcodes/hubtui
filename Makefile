BIN     := bin/hubtui
PKG     := ./cmd/hubtui
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# Container image. PLATFORMS is what `make image` and `make image-push` build.
IMAGE     ?= docker.io/davidhfrankelcodes/hubtui
PLATFORMS ?= linux/amd64,linux/arm64

export CGO_ENABLED := 0

# VHS renders the README demo; its image bundles ttyd, ffmpeg and a browser.
VHS_IMAGE := ghcr.io/charmbracelet/vhs:v0.12.1@sha256:ea49a6a1c529be83153e88321892b5585964418f0b9055e8c1e0d732194234a3

.PHONY: build run test lint fmt clean release image image-push demo

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

# Archives for linux and darwin on amd64 and arm64, plus SHA256SUMS, in dist/.
release:
	VERSION='$(VERSION)' LDFLAGS='$(LDFLAGS)' ./scripts/release.sh

# Builds the image for every platform without publishing it.
image:
	docker buildx build --platform $(PLATFORMS) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

# Publishes the image as both the version and latest; expects docker login.
image-push:
	docker buildx build --platform $(PLATFORMS) --build-arg VERSION=$(VERSION) \
		-t $(IMAGE):$(VERSION) -t $(IMAGE):latest --push .

# Records docs/demo.gif from docs/demo.tape. The binary is built for Linux on
# the host's architecture, which is what Docker runs the VHS container as.
demo:
	GOOS=linux go build -trimpath -ldflags '$(LDFLAGS)' -o bin/demo/hubtui $(PKG)
	docker run --rm -u "$$(id -u):$$(id -g)" -e HOME=/tmp -v "$(CURDIR)":/vhs -w /vhs $(VHS_IMAGE) docs/demo.tape

clean:
	rm -rf bin dist
