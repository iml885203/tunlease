# vhs script for the Tunlease demo GIF.
# Render with:  scripts/record-demo.sh   (starts the gateway, then runs vhs)
# Output:       assets/demo.gif
#
# Deliberately minimal (like bore's demo): one command, and the tunnel is up.
# The gateway and a local stand-in server are started by scripts/record-demo.sh
# BEFORE vhs runs, so this tape shows only the `tunle claim` command and its
# output. The user gives just the domain — the client appends the control-plane
# prefix automatically.

Output assets/demo.gif

Set Shell bash
Set FontSize 18
Set Width 1120
Set Height 240
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 45ms

# The local demo gateway has no TLS, so default the scheme to http off-camera.
# A real, TLS-terminated gateway needs neither this nor an explicit scheme —
# `--gateway myapp.example.com` defaults to https.
Env TUNLEASE_DEFAULT_SCHEME "http"

Sleep 500ms
Type "tunle claim /webhooks/stripe/* --to 3007 --gateway tunlease.demo"
Enter
Sleep 4s
