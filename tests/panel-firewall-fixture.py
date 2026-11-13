"""Disposable routed-packet regression; requires Docker and exported Go policies."""
import argparse
import json
import pathlib
import subprocess
import uuid

parser = argparse.ArgumentParser()
parser.add_argument("--policy-dir", required=True)
parser.add_argument("--context", default="desktop-linux")
args = parser.parse_args()
policy = pathlib.Path(args.policy_dir).resolve()
prefix = "rm-panel-fw-" + uuid.uuid4().hex
image = "remnamanager-panel-firewall-test:local"
containers = []
network = None


def docker(*argv, stdin=None):
    return subprocess.check_output(
        ["docker", "--context", args.context, *argv],
        input=stdin.encode() if stdin is not None else None,
    ).decode().strip()


server = r"""
import http.server,threading,socket,time
class Handler(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  self.send_response(200);self.end_headers();self.wfile.write(b'ok')
 def log_message(self,*args): pass
for port in (80,443,2222,3000,3100):
 s=http.server.ThreadingHTTPServer(('0.0.0.0',port),Handler)
 threading.Thread(target=s.serve_forever,daemon=True).start()
def udp():
 s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.bind(('0.0.0.0',443))
 while True:
  data,addr=s.recvfrom(4096);s.sendto(data,addr)
threading.Thread(target=udp,daemon=True).start()
while True: time.sleep(60)
"""
probe = r"""
import socket,sys,time,urllib.request
host,port,protocol,expected=sys.argv[1],int(sys.argv[2]),sys.argv[3],sys.argv[4]=='open'
opened=False
for _ in range(3):
 try:
  if protocol=='udp':
   with socket.socket(socket.AF_INET,socket.SOCK_DGRAM) as s:
    s.settimeout(.5);s.sendto(b'probe',(host,port));opened=s.recv(100)==b'probe'
  else:
   with urllib.request.urlopen('http://%s:%s/'%(host,port),timeout=.5) as r: opened=r.status==200
 except OSError: opened=False
 if opened==expected: break
 time.sleep(.1)
assert opened==expected,(host,port,protocol,expected,opened)
"""


def check(port, expected, protocol="tcp"):
    docker("exec", client, "python3", "-c", probe, router_ip, str(port), protocol, expected)


try:
    network = docker("network", "create", prefix)
    for role in ("router", "backend", "client"):
        options = ["run", "-d", "--name", prefix + "-" + role, "--network", network]
        if role == "router":
            options += ["--cap-add", "NET_ADMIN", "--sysctl", "net.ipv4.ip_forward=1"]
        cid = docker(*options, image, "python3", "-u", "-c", server)
        containers.append(cid)
    router, backend, client = containers

    def address(cid):
        info = json.loads(docker("inspect", cid))[0]
        return next(iter(info["NetworkSettings"]["Networks"].values()))["IPAddress"]

    router_ip, backend_ip = address(router), address(backend)
    nat = "table ip fixture_nat {\n chain prerouting { type nat hook prerouting priority dstnat; policy accept;\n"
    for port, target in ((80, 80), (443, 443), (9999, 443), (3000, 3000)):
        nat += f"tcp dport {port} dnat to {backend_ip}:{target}\n"
    for port in (443, 9999):
        nat += f"udp dport {port} dnat to {backend_ip}:443\n"
    nat += "}\n chain postrouting { type nat hook postrouting priority srcnat; policy accept; masquerade; }\n}\n"
    docker("exec", "-i", router, "nft", "-f", "-", stdin=nat)
    check(443, "open")
    check(9999, "open")
    check(2222, "open")
    docker("exec", "-i", router, "nft", "-f", "-", stdin=(policy / "old.nft").read_text())
    check(443, "blocked")
    for name in ("new.nft", "caddy.nft"):
        replacement = "delete table inet remnamanager_go\n" + (policy / name).read_text()
        docker("exec", "-i", router, "nft", "-c", "-f", "-", stdin=replacement)
        docker("exec", "-i", router, "nft", "-f", "-", stdin=replacement)
        for port in (80, 443, 2222):
            check(port, "open")
        for port in (9999, 3000, 3100):
            check(port, "blocked")
        check(443, "open" if name == "caddy.nft" else "blocked", "udp")
        check(9999, "blocked", "udp")
        docker("exec", router, "nft", "list", "table", "ip", "fixture_nat")
    print("PASS: old policy blocks published HTTPS; fixed policies allow HTTP/HTTPS, preserve SSH, block backend and alternate DNAT ports; Caddy UDP 443 works; unrelated NAT survives.")
finally:
    for cid in containers:
        docker("rm", "-f", cid)
    if network:
        docker("network", "rm", network)
