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

# Narrow to 17.x and 18.x Alpine releases with a regex, then to arm64.
Type "/"
Sleep 300ms
Type "^1[78]\.[0-9]+-alpine$"
Sleep 1.5s
Enter
Sleep 1s
Type "a"
Sleep 700ms
Type "a"
Sleep 1.5s

# Sort by name (17.9 before 17.10), then look inside the newest.
Type "s"
Sleep 1.5s
Type "G"
Sleep 1s
Enter
Sleep 2.5s

# Copy a pinned, multi-arch reference.
Type "Y"
Sleep 3s

Type "?"
Sleep 3s
Type "x"
Sleep 500ms
Type "q"
Sleep 500ms
