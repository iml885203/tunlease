# vhs script for the tunlease developer-flow demo GIF.
# Render with:  vhs assets/demo.tape   (brew install vhs)
# Output:       assets/demo.gif
#
# One idea, three beats: the same staging URL 404s before the claim and
# reaches a server on your laptop after it. Run against a live gateway
# with the `tunlease` CLI on PATH and a token-free ~/.tunlease.yaml.
#
# Precondition (not shown): a local server on :8080 answers
# "hello from my laptop :8080". Start it before recording.

Output assets/demo.gif

Set Shell bash
Set FontSize 18
Set Width 1180
Set Height 400
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 45ms

# 1. BEFORE — the staging URL does not reach your laptop
Type "curl -s -o /dev/null -w 'before -> HTTP %{http_code}\n' https://tunlease.example.com/integration/demo/hi"
Enter
Sleep 1.8s

# 2. CLAIM the path (backgrounded so we can curl while the tunnel holds)
Type "tunlease claim --to 8080 /integration/demo/* & sleep 2"
Enter
Sleep 3s

# 3. AFTER — the same URL now lands on your laptop
Type "curl -s https://tunlease.example.com/integration/demo/hi"
Enter
Sleep 4.5s
