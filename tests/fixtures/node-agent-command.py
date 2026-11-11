#!/usr/bin/python3
"""Isolated agent test doubles. Requires the explicitly opted-in test container."""
import json
import os
import pathlib
import shutil
import signal
import subprocess
import sys
import urllib.request

if os.environ.get("RM_AGENT_STAGES_INTEGRATION") != "1" or not pathlib.Path("/.dockerenv").exists():
    raise SystemExit("Node agent fixture commands require the disposable test container")

name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
state = pathlib.Path("/var/lib/remnamanager-node-fixture")
state.mkdir(mode=0o700, exist_ok=True)
timer = state / "timer"

def fixture_certificate(directory, domain):
    directory.mkdir(parents=True, exist_ok=True)
    subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", f"/CN={domain}", "-addext", f"subjectAltName=DNS:{domain}", "-keyout", str(directory / "privkey.pem"), "-out", str(directory / "fullchain.pem")], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

if name == "curl" and os.environ.get("RM_AGENT_V2_INTEGRATION") == "1":
    if "https://raw.githubusercontent.com/acmesh-official/acme.sh/807da6498377ee5e0cf43a78091f46f12dc59a89/acme.sh" in args:
        shutil.copyfile(os.environ.get("RM_ACME_DOWNLOAD_SOURCE","/opt/acme.sh.fixture"), args[args.index("-o")+1])
    elif os.environ.get("RM_REAL_ACME_INTEGRATION") == "1":
        os.execv("/usr/bin/curl",["/usr/bin/curl"]+args)
    else:
        raise SystemExit("Unexpected network request in mocked ACME fixture")
elif name == "sh":
    if not args or not args[0].endswith("/acme.sh") or os.environ.get("RM_AGENT_V2_INTEGRATION") != "1" or os.environ.get("RM_REAL_ACME_INTEGRATION") == "1":
        os.execv("/bin/sh", ["/bin/sh"]+args)
    def option(key): return args[args.index(key)+1]
    home = pathlib.Path(option("--home"))
    domain = "cover.fixture.test"
    certificate = state / "certificate"
    if "--register-account" in args:
        (state / "acme-account").write_text("registered")
    elif "--issue" in args:
        assert option("--keylength") == "ec-256" and option("--server") == "letsencrypt"
        webroot = pathlib.Path(option("--webroot"))
        token = webroot / ".well-known/acme-challenge/fixture-token"
        token.parent.mkdir(parents=True, exist_ok=True)
        token.write_text("acme-http-01-fixture")
        token.chmod(0o644)
        request = urllib.request.Request("http://127.0.0.1/.well-known/acme-challenge/fixture-token", headers={"Host":domain})
        assert urllib.request.urlopen(request, timeout=5).read() == b"acme-http-01-fixture"
        token.unlink()
        if (certificate / "fullchain.pem").exists(): raise SystemExit(2)
        fixture_certificate(certificate,domain)
    elif "--install-cert" in args:
        renewal = {"fullchain":option("--fullchain-file"),"key":option("--key-file"),"reload":option("--reloadcmd")}
        (state / "renewal.json").write_text(json.dumps(renewal))
        shutil.copyfile(certificate / "fullchain.pem",renewal["fullchain"])
        shutil.copyfile(certificate / "privkey.pem",renewal["key"])
        subprocess.run(["/bin/sh",renewal["reload"]],check=True)
    elif "--cron" in args:
        renewal=json.loads((state / "renewal.json").read_text())
        fixture_certificate(certificate,domain)
        shutil.copyfile(certificate / "fullchain.pem",renewal["fullchain"])
        shutil.copyfile(certificate / "privkey.pem",renewal["key"])
        subprocess.run(["/bin/sh",renewal["reload"]],check=True)
        (state / "renewed").write_text("yes")
    else: raise SystemExit("Unexpected ACME fixture command")
elif name == "dpkg-query":
    print("install ok installed")
elif name == "docker":
    if args[:1] == ["version"]:
        print("28.5.0")
    elif args[:2] == ["image", "inspect"]:
        if "{{.Id}}" in args:
            print("sha256:" + args[-1].split("sha256:")[-1])
        else:
            print("remnawave/node@sha256:" + ("b" if (state / "update-mode").exists() else "a") * 64)
    elif args[:1] == ["inspect"]:
        if (state / "missing-node").exists():
            raise SystemExit(1)
        print("true" if "{{.State.Running}}" in args else (state / "running-image").read_text())
    elif args[:1] == ["compose"]:
        if "ps" in args:
            print("" if (state / "missing-node").exists() else "fixture-node-container")
        if "up" in args:
            if (state / "require-recreate").exists():
                assert "--force-recreate" in args
            doc = json.loads(pathlib.Path("/opt/remnanode/compose.json").read_text())
            image = doc["services"]["remnanode"]["image"].split("sha256:")[-1]
            if (state / "update-fail").exists() and image == "b" * 64:
                (state / "update-fail").unlink()
                raise SystemExit(1)
            (state / "running-image").write_text("sha256:" + image)
            (state / "missing-node").unlink(missing_ok=True)
        if "config" in args:
            doc = json.loads(pathlib.Path("/opt/remnanode/compose.json").read_text())
            assert doc["services"]["remnanode"]["network_mode"] == "host"
    elif args[:1] == ["pull"]:
        (state / "last-pull").write_text(args[-1])
    else:
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
    command = "sleep 120; /usr/sbin/nft -f /var/lib/remnamanager-node/firewall-rollback.nft; rm -f /var/lib/remnamanager-node-fixture/timer"
    if "--on-active=300s" in args:
        command = "sleep 300; /bin/bash " + args[-1]
    process = subprocess.Popen(["/bin/sh", "-c", command], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, close_fds=True, start_new_session=True)
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
    elif args[:2] == ["start", "remnamanager-node-acme.service"]:
        subprocess.run(["sh","/var/lib/remnamanager-node/acme/acme.sh","--cron","--home","/var/lib/remnamanager-node/acme","--config-home","/var/lib/remnamanager-node/acme"],check=True)
    elif args[0] not in ("enable", "disable", "daemon-reload", "reset-failed"):
        raise SystemExit("Unexpected fixture systemctl command")
else:
    raise SystemExit("Unexpected fixture command")
