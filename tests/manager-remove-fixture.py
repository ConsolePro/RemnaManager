"""Exercise real installed-manager removal through its TUI in a disposable container."""
import argparse
import errno
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import shutil
import struct
import subprocess
import termios
import time

if os.environ.get("RM_CONTAINER_TESTS") != "1" or not Path("/.dockerenv").exists() or os.geteuid() != 0:
    raise SystemExit("Only run with RM_CONTAINER_TESTS=1 in a disposable root Linux container")

parser = argparse.ArgumentParser()
parser.add_argument("--binary", default="/usr/local/lib/remnamanager/remanager")
args = parser.parse_args()
target = Path("/usr/local/lib/remnamanager/remanager")
target.parent.mkdir(parents=True, exist_ok=True)
if Path(args.binary).resolve() != target:
    shutil.copyfile(args.binary, target)
target.chmod(0o755)
for name in ("remanager.previous", "remna.previous"):
    shutil.copyfile(target, target.parent / name)
(target.parent / "remanager.previous.json").write_text('{"Version":"v1.3.0"}')
for name in ("install.lock", "remanager.update.lock"):
    (target.parent / name).touch()
configuration = Path("/etc/remnamanager")
configuration.mkdir(mode=0o700, parents=True, exist_ok=True)
(configuration / "config.json").write_text(json.dumps({"language": "en", "email": "test@example.com", "initialized": True}))
(configuration / "update-cache.json").write_text('{"tag_name":"v1.3.1","checked":"2099-01-01T00:00:00Z"}')
commands = Path("/usr/local/bin")
commands.mkdir(parents=True, exist_ok=True)
for name, link in (("remanager", "../lib/remnamanager/remanager"), ("re", "remanager"), ("remna_manager", "remanager")):
    path = commands / name
    path.unlink(missing_ok=True)
    path.symlink_to(link)

preserved = {
    "/usr/local/bin/remna": b"#!/bin/sh\necho panel-cli\n",
    "/opt/remnawave/compose.json": b"panel compose must survive",
    "/opt/remnanode/compose.json": b"node compose must survive",
    "/var/lib/remnamanager/panel.json": b"existing panel state must survive",
    "/var/lib/remnamanager/firewall-apply.sh": b"firewall boot script",
    "/var/lib/remnamanager/firewall-active.nft": b"firewall rules",
    "/var/lib/remnamanager/nodes/node.json": b"node journal",
    "/var/lib/remnamanager/ssh/keys/node-test": b"managed SSH key",
    "/var/lib/remnamanager/backups/db.sql": b"database backup",
    "/etc/systemd/system/remnamanager-firewall.service": b"firewall service",
    "/etc/letsencrypt/renewal-hooks/deploy/remnamanager-go-nginx": b"certificate renewal hook",
}
for name, data in preserved.items():
    path = Path(name)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)
log = Path("/var/lib/remnamanager/management-123.log")
log.write_text("old management log")

def launch():
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 42, 120, 0, 0))
    process = subprocess.Popen([str(target)], stdin=slave, stdout=slave, stderr=slave, env=dict(os.environ, TERM="xterm-256color", COLORTERM="truecolor"))
    os.close(slave)
    return process, master

process, master = launch()
transcript = bytearray()

def read_until(token, timeout=15):
    deadline = time.monotonic() + timeout
    chunk = bytearray()
    while time.monotonic() < deadline:
        if select.select([master], [], [], 0.1)[0]:
            try:
                data = os.read(master, 65536)
            except OSError as error:
                if error.errno == errno.EIO:
                    break
                raise
            if not data:
                break
            chunk.extend(data)
            transcript.extend(data)
            if token in chunk:
                return
    raise AssertionError("Missing terminal text: " + repr(token) + "\n" + bytes(transcript[-5000:]).decode(errors="replace"))

def open_removal():
    read_until(b"Main menu")
    os.write(master, b"\x1b[A")  # Settings is the last root section.
    time.sleep(0.1)
    os.write(master, b"\r")
    read_until(b"Remove manager from server")
    os.write(master, b"\x1b[B")
    time.sleep(0.1)
    os.write(master, b"\r")
    read_until(b"remove manager and exit")

try:
    open_removal()
    assert target.exists() and (configuration / "config.json").exists(), "removed before confirmation"
    os.write(master, b"\x1b")
    read_until(b"Remove manager from server")
    assert target.exists(), "cancel removed manager"
    os.write(master, b"\r")
    read_until(b"remove manager and exit")
    with (target.parent / "remanager.update.lock").open("r+") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        os.write(master, b"\r")
        read_until(b"Another installation or update is running")
        assert process.wait(timeout=10) != 0
        assert target.exists() and (configuration / "config.json").exists(), "busy update lock did not block removal"
    os.close(master)
    process, master = launch()
    open_removal()
    os.write(master, b"\r")
    read_until(b"RemnaManager removed.")
    assert process.wait(timeout=10) == 0
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    os.close(master)

for name, data in preserved.items():
    assert Path(name).read_bytes() == data, "component file changed: " + name
for name in ("re", "remanager", "remna_manager"):
    assert not os.path.lexists(commands / name), "manager command remains: " + name
assert not target.parent.exists(), "manager executables or locks remain"
assert not configuration.exists(), "manager configuration or cache was recreated"
assert not log.exists(), "manager log remains"
print("PASS: real TUI cancel/confirm, busy update lock blocks removal, self-removal after exit, panel/node/firewall/certificates/SSH state preserved")
