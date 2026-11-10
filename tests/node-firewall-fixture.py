"""Disposable Docker test helper; never runs on the production node."""
import http.server
import sys
import threading
import time
import urllib.request


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"ok")

    def log_message(self, *_):
        pass


if sys.argv[1] == "serve":
    for port in (80, 2200, 2222, 9000):
        server = http.server.ThreadingHTTPServer(("0.0.0.0", port), Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
    while True:
        time.sleep(60)
elif sys.argv[1] == "probe":
    host, port, expected = sys.argv[2], int(sys.argv[3]), sys.argv[4] == "open"
    opened = False
    for attempt in range(3):
        try:
            with urllib.request.urlopen(f"http://{host}:{port}/", timeout=0.6) as response:
                opened = response.status == 200
        except (OSError, TimeoutError):
            opened = False
        if opened == expected:
            break
        time.sleep(0.1)
    if opened != expected:
        raise SystemExit(f"port {port}: expected {'open' if expected else 'blocked'}")
