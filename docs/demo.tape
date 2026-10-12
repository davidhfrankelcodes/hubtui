# Records docs/demo.gif. Run `make demo`; it builds a Linux binary into
# bin/demo/ and runs VHS in its container, so nothing needs installing.
# The demo talks to the live Docker Hub, so results drift between runs.

Output docs/demo.gif

Set Shell "bash"
Set FontSize 16
Set Width 1200
Set Height 640
Set Padding 16
Set Theme "Catppuccin Mocha"
Set TypingSpeed 70ms

Hide
Type "export PATH=$PWD/bin/demo:$PATH && clear"
Enter
Show

# Search, then open the top result. Official images reliably rank first,
# which keeps the scripted keys pointing at the same repository.
Type "hubtui"
Enter
Sleep 1s
Type "postgres"
Sleep 2.5s
Enter
Sleep 1s
Enter
Sleep 3s

# Hide betas and release candidates, then group by major version. Grouping
# loads every page, so give it time. g picks the newest major, whatever
# that is on the day of recording.
Type "u"
Sleep 1.5s
Type "v"
Sleep 4s
Type "g"
Sleep 1s
Enter
Sleep 2s

# Within that major, Alpine images on arm64.
Type "/"
Sleep 300ms
Type "alpine$"
Sleep 1.5s
Enter
Sleep 1s
Type "a"
Sleep 700ms
Type "a"
Sleep 2s

# Look inside the newest: per-platform digests and the tags that alias it.
Enter
Sleep 3s

# Copy a pinned, multi-arch reference.
Type "Y"
Sleep 3s

Type "?"
Sleep 3s
Type "x"
Sleep 500ms
Type "q"
Sleep 1s

# The same data without the UI: a table by default, --json for scripts.
Type "hubtui tags postgres --stable --limit 8"
Sleep 500ms
Enter
Sleep 5s
