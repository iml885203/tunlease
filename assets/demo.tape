# vhs script for the Tunlease demo GIF.
# Render with:  scripts/record-demo.sh   (starts the gateway, then runs vhs)
# Output:       assets/demo.gif
#
# Deliberately minimal (like bore's demo): one command, and the tunnel is up.
# The gateway and a local stand-in server are started by scripts/record-demo.sh
# BEFORE vhs runs, so this tape shows only `tunle claim` and its output.
# Precondition (handled by record-demo.sh): a gateway on :8080 with /webhooks/
# allowed, a local server on :3007, and `tunle` on PATH.

Output assets/demo.gif

Set Shell bash
Set FontSize 18
Set Width 1120
Set Height 240
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 45ms

Sleep 500ms
Type "tunle claim /webhooks/stripe/* --to 3007 --gateway http://localhost:8080"
Enter
Sleep 4s
