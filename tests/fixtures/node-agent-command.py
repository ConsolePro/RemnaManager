#!/usr/bin/python3
"""Isolated agent test doubles. Requires the explicitly opted-in test container."""
import json
import os
import pathlib
import shutil
import signal
import subprocess
import sys

if os.environ.get("RM_AGENT_STAGES_INTEGRATION") != "1" or not pathlib.Path("/.dockerenv").exists():
    raise SystemExit("Node agent fixture commands require the disposable test container")

name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
state = pathlib.Path("/var/lib/remnamanager-node-fixture")
state.mkdir(mode=0o700, exist_ok=True)
timer = state / "timer"

if name == "dpkg-query":
    print("install ok installed")
elif name == "docker":
    if args[:1] == ["version"]:
        print("28.5.0")
    elif args[:2] == ["image", "inspect"]:
        print("remnawave/node@sha256:" + "a" * 64)
    elif args[:1] == ["compose"]:
        if "config" in args:
            doc = json.loads(pathlib.Path("/opt/remnanode/compose.json").read_text())
            assert doc["services"]["remnanode"]["network_mode"] == "host"
    elif args[:1] != ["pull"]:
        raise SystemExit("Unexpected fixture Docker command")
elif name == "getent":
    print("192.0.2.10 STREAM cover.fixture.test")
elif name == "certbot":
    domain = args[args.index("-d") + 1]
    directory = pathlib.Path("/etc/letsencrypt/live") / domain
    directory.mkdir(parents=True, exist_ok=True)
    if not (directory / "fullchain.pem").exists():
        subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", f"/CN={domain}", "-addext", f"subjectAltName=DNS:{domain}", "-keyout", str(directory / "privkey.pem"), "-out", str(directory / "fullchain.pem")], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
elif name == "systemd-run":
    # Preserve the independent timer/process model; tests may force early expiry.
    timer.write_text("active")
    process = subprocess.Popen(["/bin/sh", "-c", "sleep 120; /usr/sbin/nft -f /var/lib/remnamanager-node/firewall-rollback.nft; rm -f /var/lib/remnamanager-node-fixture/timer"], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, close_fds=True, start_new_session=True)
    (state / "timer.pid").write_text(str(process.pid))
elif name == "systemctl":
    if args[0] == "is-active":
        if "firewalld" in args or not timer.exists():
            raise SystemExit(3)
    elif args[0] in ("stop", "fixture-expire"):
        pid = state / "timer.pid"
        if pid.exists():
            try:
                os.killpg(int(pid.read_text()), signal.SIGTERM)
            except ProcessLookupError:
                pass
            pid.unlink()
        if args[0] == "fixture-expire":
            subprocess.run(["/usr/sbin/nft", "-f", "/var/lib/remnamanager-node/firewall-rollback.nft"], check=True)
        timer.unlink(missing_ok=True)
    elif args[0] == "enable" and "docker" in args:
        if not pathlib.Path("/etc/nginx").exists():
            shutil.copytree("/opt/fixture-nginx", "/etc/nginx")
        pathlib.Path("/etc/letsencrypt/renewal-hooks/deploy").mkdir(parents=True, exist_ok=True)
    elif args[0] == "enable" and "nginx" in args:
        pid_file = pathlib.Path("/run/nginx.pid")
        if not pid_file.exists() or not pid_file.read_text().strip():
            subprocess.run(["/usr/sbin/nginx"], check=True)
    elif args[:2] == ["reload", "nginx"]:
        subprocess.run(["/usr/sbin/nginx", "-s", "reload"], check=True)
    elif args[0] not in ("enable", "daemon-reload", "reset-failed"):
        raise SystemExit("Unexpected fixture systemctl command")
else:
    raise SystemExit("Unexpected fixture command")
