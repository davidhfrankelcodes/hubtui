#!/usr/bin/env bash
# Builds release archives and checksums for every supported platform into
# dist/. Run via `make release`, which supplies VERSION and LDFLAGS.
set -euo pipefail

VERSION="${VERSION:?VERSION must be set}"
LDFLAGS="${LDFLAGS:?LDFLAGS must be set}"
PLATFORMS=(linux/amd64 linux/arm64 darwin/amd64 darwin/arm64)
DIST=dist

clean() {
	echo "[1/4] Cleaning ${DIST}/"
	rm -rf "${DIST}"
	mkdir -p "${DIST}"
}

build_one() {
	local os="${1%/*}" arch="${1#*/}"
	local name="hubtui_${VERSION}_${os}_${arch}"
	echo "      ${os}/${arch}"
	mkdir -p "${DIST}/${name}"
	CGO_ENABLED=0 GOOS="${os}" GOARCH="${arch}" \
		go build -trimpath -ldflags "${LDFLAGS}" -o "${DIST}/${name}/hubtui" ./cmd/hubtui
}

build_all() {
	echo "[2/4] Building ${#PLATFORMS[@]} binaries (${VERSION})"
	local p
	for p in "${PLATFORMS[@]}"; do
		build_one "${p}"
	done
}

archive_all() {
	echo "[3/4] Archiving"
	local dir
	for dir in "${DIST}"/hubtui_*/; do
		dir="$(basename "${dir}")"
		tar -C "${DIST}" -czf "${DIST}/${dir}.tar.gz" "${dir}"
		rm -rf "${DIST:?}/${dir}"
	done
}

# sha256sum is GNU; macOS ships shasum instead.
sha256() {
	if command -v sha256sum >/dev/null; then
		sha256sum "$@"
	else
		shasum -a 256 "$@"
	fi
}

checksums() {
	echo "[4/4] Writing ${DIST}/SHA256SUMS"
	(cd "${DIST}" && sha256 ./*.tar.gz | sed 's| \./| |' >SHA256SUMS)
}

main() {
	clean
	build_all
	archive_all
	checksums
	echo "Done:"
	ls -1 "${DIST}"
}

main "$@"
