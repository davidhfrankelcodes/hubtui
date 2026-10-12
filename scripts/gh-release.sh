#!/usr/bin/env bash
# Publishes a GitHub release for VERSION with the archives and SHA256SUMS
# from `make release`, and the version's CHANGELOG.md entry as its notes.
# Run via `make gh-release`, which supplies VERSION, GH, GH_REPO and IMAGE.
# Needs GH_TOKEN (or a gh login) with write access to the repository.
#
# Rerunning for an existing release replaces its assets rather than failing,
# so a retried CI build converges instead of needing manual cleanup.
set -euo pipefail

VERSION="${VERSION:?VERSION must be set}"
GH="${GH:?GH must be set}"
GH_REPO="${GH_REPO:?GH_REPO must be set}"
IMAGE="${IMAGE:?IMAGE must be set}"
DIST="${DIST:-dist}"
PLATFORMS=(linux_amd64 linux_arm64 darwin_amd64 darwin_arm64)
export GH_REPO

# GH may be a multi-word command such as `go run .../gh@v2.102.0`.
read -r -a gh_cmd <<<"${GH}"
gh() { "${gh_cmd[@]}" "$@"; }

ASSETS=()
NOTES=""

# sha256sum is GNU; macOS ships shasum instead.
sha256_check() {
	if command -v sha256sum >/dev/null; then
		sha256sum --check --quiet "$@"
	else
		shasum -a 256 --check --quiet "$@"
	fi
}

check_assets() {
	echo "[1/4] Checking ${DIST}/ holds the ${VERSION} archives"
	local p archive
	for p in "${PLATFORMS[@]}"; do
		archive="${DIST}/hubtui_${VERSION}_${p}.tar.gz"
		if [[ ! -f "${archive}" ]]; then
			echo "error: ${archive} is missing; run make release VERSION=${VERSION} first" >&2
			exit 1
		fi
		ASSETS+=("${archive}")
	done
	(cd "${DIST}" && sha256_check SHA256SUMS)
	ASSETS+=("${DIST}/SHA256SUMS")
}

write_notes() {
	echo "[2/4] Reading the ${VERSION} entry from CHANGELOG.md"
	local entry
	entry="$(./scripts/changelog.sh "${VERSION}")"
	if [[ -z "${entry}" ]]; then
		echo "error: CHANGELOG.md has no entry for ${VERSION}" >&2
		exit 1
	fi
	NOTES="$(printf '%s\n\n### Install\n\nDownload the archive for your platform below and check it against `SHA256SUMS`, or run the container image:\n\n```sh\ndocker run -it --rm %s:%s nginx\n```\n' "${entry}" "${IMAGE}" "${VERSION}")"
}

publish() {
	echo "[3/4] Publishing release ${VERSION} to ${GH_REPO}"
	if gh release view "${VERSION}" >/dev/null 2>&1; then
		echo "      exists; replacing its notes and assets"
		gh release edit "${VERSION}" --notes "${NOTES}"
		gh release upload "${VERSION}" "${ASSETS[@]}" --clobber
		return
	fi
	local flags=()
	# Prereleases must not become the "Latest" release.
	if [[ "${VERSION}" == *-* ]]; then
		flags+=(--prerelease --latest=false)
	fi
	# --verify-tag refuses to create the tag on the fly, so a release only
	# ever names a tag that make tag created and someone pushed. The flags
	# expansion tolerates an empty array under set -u on macOS's bash 3.2.
	gh release create "${VERSION}" "${ASSETS[@]}" \
		--verify-tag --title "hubtui ${VERSION}" --notes "${NOTES}" ${flags[@]+"${flags[@]}"}
}

report() {
	echo "[4/4] Done"
	gh release view "${VERSION}" --json url --jq .url
}

main() {
	check_assets
	write_notes
	publish
	report
}

main "$@"
