"""Tiny message relay between Claude Code on a host and Claude Code agents in test VMs.

Usage: python3 -I relay.py [--keep-token] ADDRESS [ADDRESS ...]
Listens on port 8765 on each address (include 127.0.0.1 for send.sh). See README.md.

Agent:  GET  /next?agent=NAME&wait=SECONDS -> next message for NAME (204 if none by then)
        POST /reply?agent=NAME             -> body stored as outbox/<time>-NAME.txt
Claude: POST /send?agent=NAME (localhost)  -> queues a message for NAME
Every request needs header X-Relay-Token. Messages are plain text.
"""
import os, sys, time, threading
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler
from urllib.parse import urlparse, parse_qs

ROOT = os.path.dirname(os.path.abspath(__file__))
INBOX, OUTBOX = os.path.join(ROOT, "inbox"), os.path.join(ROOT, "outbox")
os.makedirs(INBOX, exist_ok=True); os.makedirs(OUTBOX, exist_ok=True)

def load_token():
    """A fresh random token per start unless --keep-token reuses ./token.
    The person running the VM gives it to the agent (never commit it)."""
    path = os.path.join(ROOT, "token")
    if "--keep-token" in sys.argv and os.path.exists(path):
        return open(path).read().strip()
    import secrets
    tok = secrets.token_hex(16)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    os.write(fd, tok.encode()); os.close(fd)
    return tok
TOKEN = load_token()
lock = threading.Lock()

import re
def agent_of(u):
    a = parse_qs(u.query).get("agent", [""])[0]
    return a if re.fullmatch(r"[a-z0-9-]{1,20}", a) else None

def next_message(agent):
    box = os.path.join(INBOX, agent)
    os.makedirs(box, exist_ok=True)
    with lock:
        names = sorted(os.listdir(box))
        if not names:
            return None
        path = os.path.join(box, names[0])
        body = open(path, "rb").read()
        os.rename(path, os.path.join(ROOT, "delivered-%s-%s" % (agent, names[0])))
        return body

class H(BaseHTTPRequestHandler):
    def _auth(self):
        if self.headers.get("X-Relay-Token") != TOKEN:
            self.send_response(403); self.end_headers(); return False
        return True
    def _send(self, code, body=b""):
        self.send_response(code)
        self.send_header("Content-Type", "text/plain; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers(); self.wfile.write(body)
    def do_GET(self):
        if not self._auth(): return
        u = urlparse(self.path)
        agent = agent_of(u)
        if u.path != "/next" or not agent: return self._send(404)
        wait = min(float(parse_qs(u.query).get("wait", ["0"])[0]), 600)
        end = time.time() + wait
        while True:
            m = next_message(agent)
            if m is not None: return self._send(200, m)
            if time.time() >= end: return self._send(204)
            time.sleep(1)
    def do_POST(self):
        if not self._auth(): return
        u = urlparse(self.path)
        agent = agent_of(u)
        if not agent: return self._send(404)
        n = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(n)
        name = time.strftime("%Y%m%dT%H%M%S") + "-%06d-%s.txt" % (time.time_ns() // 1000 % 1000000, agent)
        if u.path == "/reply":
            open(os.path.join(OUTBOX, name), "wb").write(body); return self._send(200, b"ok")
        if u.path == "/send" and self.client_address[0] == "127.0.0.1":
            os.makedirs(os.path.join(INBOX, agent), exist_ok=True)
            open(os.path.join(INBOX, agent, name), "wb").write(body); return self._send(200, b"queued")
        self._send(404)
    def log_message(self, fmt, *a):
        sys.stderr.write("%s %s\n" % (time.strftime("%H:%M:%S"), fmt % a))

if __name__ == "__main__":
    for host in [h for h in sys.argv[1:] if not h.startswith("--")]:
        s = ThreadingHTTPServer((host, 8765), H)
        threading.Thread(target=s.serve_forever, daemon=True).start()
        print("relay on", host, 8765, flush=True)
    threading.Event().wait()
