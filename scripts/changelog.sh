#!/usr/bin/env bash
# Prints the body of one version's CHANGELOG.md entry, without its heading.
# Usage: scripts/changelog.sh 1.2.3 [CHANGELOG.md]. Shared by tag.sh and
# gh-release.sh so the tag message and the release notes cannot drift apart.
set -euo pipefail

VERSION="${1:?usage: changelog.sh VERSION [FILE]}"
VERSION="${VERSION#v}"
FILE="${2:-CHANGELOG.md}"

# The entry ends at the next section or the link definitions. Blank lines
# are held back until more text follows, which trims them from both ends.
print_entry() {
	awk -v heading="## [${VERSION}] - " '
		index($0, heading) == 1 { found = 1; next }
		found && (/^## \[/ || /^\[[^]]+\]: /) { exit }
		!found { next }
		/^$/ { if (started) blanks++; next }
		{ while (blanks > 0) { print ""; blanks-- } print; started = 1 }
	' "${FILE}"
}

main() {
	print_entry
}

main "$@"
