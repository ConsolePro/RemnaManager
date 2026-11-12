# Run with the current Linux binary, initialized demo config, and a fresh
# update-cache.json mounted at /vhs. VHS runs the application in a real PTY.
# Output files stay in the recording directory; copy terminal.png to doc/assets.
Output "/vhs/session.gif"
Set Shell bash
Set Width 1320
Set Height 780
Set FontSize 20
Set FontFamily "JetBrains Mono"
Set Padding 24
Set Theme "Catppuccin Mocha"
Env TERM "xterm-256color"
Env COLORTERM "truecolor"
Hide
Type "/vhs/remanager --demo --config /vhs/config.json"
Enter
Sleep 5s
Show
Screenshot "/vhs/terminal.png"
Sleep 500ms
