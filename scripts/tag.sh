#!/usr/bin/env bash
# Creates an annotated release tag whose message is the version's CHANGELOG
# entry. Run via `make tag TAG=v1.2.3`. It never pushes: pushing the tag is
# what makes CI publish the archives and the container image, so that step
# stays a deliberate one.
set -euo pipefail

TAG="${TAG:?TAG must be set, e.g. make tag TAG=v0.1.0}"
VERSION="${TAG#v}"
BRANCH=main
CHANGELOG=CHANGELOG.md

check_format() {
	echo "[1/5] Checking ${TAG} is a release version"
	# Same rule as the Makefile's check-version, which guards publishing.
	if ! [[ "${TAG}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-(alpha|beta|rc)\.[0-9]+)?$ ]]; then
		echo "error: ${TAG} is not a version like v1.2.3 or v1.2.3-rc.1" >&2
		exit 1
	fi
}

check_repo() {
	echo "[2/5] Checking the working tree"
	local branch
	branch="$(git branch --show-current)"
	if [[ "${branch}" != "${BRANCH}" ]]; then
		echo "error: on ${branch:-a detached HEAD}; release tags go on ${BRANCH}" >&2
		exit 1
	fi
	if [[ -n "$(git status --porcelain)" ]]; then
		echo "error: the working tree has uncommitted changes" >&2
		exit 1
	fi
	if git rev-parse -q --verify "refs/tags/${TAG}" >/dev/null; then
		echo "error: ${TAG} already exists" >&2
		exit 1
	fi
}

# changelog_entry prints the body of the version's section, without its
# heading, up to the next section or the link definitions.
changelog_entry() {
	# Blank lines are held back until more text follows, which trims them
	# from both ends.
	awk -v heading="## [${VERSION}] - " '
		index($0, heading) == 1 { found = 1; next }
		found && (/^## \[/ || /^\[[^]]+\]: /) { exit }
		!found { next }
		/^$/ { if (started) blanks++; next }
		{ while (blanks > 0) { print ""; blanks-- } print; started = 1 }
	' "${CHANGELOG}"
}

check_changelog() {
	echo "[3/5] Checking ${CHANGELOG} has a dated entry for ${VERSION}"
	if ! grep -Eq "^## \[${VERSION//./\\.}\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$" "${CHANGELOG}"; then
		echo "error: ${CHANGELOG} has no \"## [${VERSION}] - YYYY-MM-DD\" heading;" >&2
		echo "       move the Unreleased notes under one and commit it first" >&2
		exit 1
	fi
	if [[ -z "$(changelog_entry)" ]]; then
		echo "error: the ${VERSION} entry in ${CHANGELOG} is empty" >&2
		exit 1
	fi
}

check_build() {
	echo "[4/5] Running make lint test"
	make lint test
}

create_tag() {
	echo "[5/5] Creating annotated tag ${TAG}"
	printf 'hubtui %s\n\n%s\n' "${TAG}" "$(changelog_entry)" | git tag -a --cleanup=whitespace "${TAG}" -F -
}

main() {
	check_format
	check_repo
	check_changelog
	check_build
	create_tag
	echo "Done. Publish with: git push origin ${TAG}"
}

main "$@"
