# Record with: make demo   (needs https://github.com/charmbracelet/vhs)
# Runs entirely against the built-in mock; no Terminus calls are made.
Output docs/demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 1400
Set Height 820
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms

Hide
Type "export XDG_CONFIG_HOME=$(mktemp -d) XDG_STATE_HOME=$(mktemp -d) XDG_CACHE_HOME=$(mktemp -d)" Enter
Type "clear" Enter
Show

Type "bin/lazyterminus --mock --env acme-web.test" Enter
Sleep 4s

# Walk the environments
Down Sleep 700ms
Down Sleep 700ms
Down Sleep 700ms
Up Sleep 700ms
Up Sleep 700ms
Up Sleep 1s

# Deploy dev → test: commit preview, note, typed confirmation
Type "d" Sleep 2s
Type "Ship trip planner" Sleep 800ms
Enter Sleep 1500ms
Type "acme-web.test" Sleep 800ms
Enter Sleep 6s

# Command palette, scoped to the selected environment
Type ":" Sleep 800ms
Type "env:info" Sleep 1s
Enter Sleep 1s
Enter Sleep 2s
Escape Sleep 800ms

# Help screen
Type "?" Sleep 2s
Escape Sleep 500ms
Type "q"
Sleep 1s
