# vhs script for the Tunlease demo GIF.
# Render with:  scripts/record-demo.sh
# Output:       assets/demo.gif
#
# Deliberately minimal (like bore's demo): one command, and the tunnel is up.
# A local stand-in server is started by scripts/record-demo.sh before vhs runs.
# The tape uses the real public demo relay and shows only the claim command and
# its output. The client appends the control-plane prefix automatically.

Output assets/demo.gif

Set Shell bash
Set FontSize 18
Set Width 1120
Set Height 240
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 45ms
Env NO_COLOR ""
Env TERM "xterm-256color"
Env FORCE_COLOR "1"

Sleep 500ms
Type "tul claim '/demo/my-tunnel/' -p 8080 -g tunlease-relay.dotw.me"
Enter
Sleep 8s
