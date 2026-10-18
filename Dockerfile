# syntax=docker/dockerfile:1

# Base images are pinned by digest; `hubtui tags golang --json` lists the
# pinned_reference for each tag when it is time to bump.
#
# The build stage always runs on the build host's platform and cross-compiles,
# so a multi-arch build needs no emulation.
FROM --platform=$BUILDPLATFORM golang:1.27.2-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
# The Makefile is not used here: the image has no make or git, and the
# version arrives as a build argument instead of from git describe.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/hubtui ./cmd/hubtui

FROM scratch
# hubtui talks to hub.docker.com over TLS, so it needs the CA bundle and
# nothing else.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/hubtui /hubtui
USER 65532:65532
ENTRYPOINT ["/hubtui"]
