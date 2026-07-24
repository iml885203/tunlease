# vhs script for the Tunlease demo GIF.
# Render with:  vhs assets/demo.tape   (brew install vhs)
# Output:       assets/demo.gif
#
# One idea, three beats: a fixed URL returns the app's 404 before a claim, then
# reaches a server on your laptop after `tunlease claim` — on the SAME URL.
#
# Self-contained: it runs `tunlease serve` (gateway + router in one process) in
# front of a tiny stand-in "app", so the recording needs no external gateway.
# Precondition: `tunlease` on PATH, plus python3 (for the two stand-in servers).

Output assets/demo.gif

Set Shell bash
Set FontSize 18
Set Width 1200
Set Height 640
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms

# --- setup (shown briefly, then cleared) ---
Type "# an 'app' on :3001, my local server on :3002, and 'tunlease serve' in front"
Enter
Sleep 800ms
Type `python3 -c "import http.server as h; h.HTTPServer(('',3001),type('A',(h.BaseHTTPRequestHandler,),{'do_GET':lambda s:(s.send_response(404),s.end_headers(),s.wfile.write(b'app: not found\n')),'log_message':lambda *a:None})).serve_forever()" &`
Enter
Type `python3 -c "import http.server as h; h.HTTPServer(('',3002),type('A',(h.BaseHTTPRequestHandler,),{'do_GET':lambda s:(s.send_response(200),s.end_headers(),s.wfile.write(b'hello from my laptop :3002\n')),'log_message':lambda *a:None})).serve_forever()" &`
Enter
Type "tunlease serve --config demo.yaml --listen :8080 --app http://127.0.0.1:3001 >/dev/null 2>&1 & sleep 1"
Enter
Sleep 1500ms
Type "clear"
Enter
Sleep 400ms

# 1. BEFORE — the fixed URL returns the app's own 404
Type "curl -s http://localhost:8080/webhooks/demo/hi"
Enter
Sleep 1800ms

# 2. CLAIM the path (backgrounded so we can curl while the tunnel holds)
Type "tunlease claim /webhooks/demo/* --to 3002 --gateway http://localhost:8080 & sleep 2"
Enter
Sleep 3s

# 3. AFTER — the SAME URL now lands on your laptop
Type "curl -s http://localhost:8080/webhooks/demo/hi"
Enter
Sleep 4s
