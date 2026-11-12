"""Run the actual Xray transaction shell code with fake Docker/systemd/downloads."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile
import zipfile

parser = argparse.ArgumentParser()
parser.add_argument("--bash", default="bash")
parser.add_argument("--jq", default="jq")
args = parser.parse_args()
repo = Path(__file__).resolve().parents[1]
job = "a" * 32
version = "v26.9.9"
image = "remnawave/node@sha256:" + "a" * 64


def case(scenario):
    (repo / ".test-state").mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="node-core-", dir=repo / ".test-state") as temporary:
        fixture = Path(temporary)
        assert fixture.resolve().is_relative_to((repo / ".test-state").resolve())
        stack = fixture / "opt/remnanode"
        root = stack / "state"
        units = fixture / "etc/systemd/system"
        for directory in (root, units, fixture / "bin"):
            directory.mkdir(parents=True)
        (root / "job-id").write_text(job)
        (root / "ready").write_text(job)
        (stack / ".remnamanager-go-node").write_text(job)
        original = {"image": image, "environment": {"SECRET_KEY": "preserve-me"}, "network_mode": "host"}
        if scenario in ("downgrade", "downgrade-failure"):
            previous = stack / "xray/v26.10.31/amd64/xray"
            previous.parent.mkdir(parents=True)
            previous.write_bytes(b"previous-core")
            original["volumes"] = [{"type": "bind", "source": previous.as_posix(), "target": "/usr/local/bin/xray", "read_only": True}]
        before = json.dumps({"services": {"remnanode": original}})
        (stack / "compose.json").write_text(before)
        archive = fixture / "source.zip"
        with zipfile.ZipFile(archive, "w") as zipped:
            zipped.writestr("xray", b"\x7fELFfixture-xray")
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        if scenario == "bad-checksum":
            digest = "b" * 64
        for name in ("flock", "systemctl", "systemd-run", "uname", "id", "curl", "jq", "sleep"):
            body = "#!/bin/sh\n"
            if name == "uname":
                body += "echo x86_64\n"
            elif name == "id":
                body += "echo 0\n"
            elif name == "curl":
                body += 'while [ "$1" != "-o" ]; do shift; done\nshift\n'
                body += 'cp ' + shlex.quote(archive.as_posix()) + ' "$1"\n'
            elif name == "jq":
                body += "exec " + shlex.quote(Path(args.jq).as_posix() if args.jq != "jq" else (shutil.which("jq") or "jq")) + ' "$@"\n'
            else:
                body += "exit 0\n"
            path = fixture / "bin" / name
            path.write_text(body)
            path.chmod(0o755)
        docker = fixture / "bin/docker"
        docker.write_text('''#!/usr/bin/env python
import hashlib, json, os, sys
from pathlib import Path
args=sys.argv[1:]
stack=Path(os.environ['RM_FIXTURE_STACK'])
if args[:2]==['exec','-i']: args.pop(1)
node=json.loads((stack/'compose.json').read_text())['services']['remnanode']
source=next((volume['source'] for volume in node.get('volumes',[]) if volume['target']=='/usr/local/bin/xray'),None)
current=Path(source).read_bytes() if source else b'bundled-core'
current_version='26.9.9' if source and '/v26.9.9/' in source else ('26.10.31' if source else '26.7.28')
if args[0]=='compose':
    if 'ps' in args: print('fixture-container')
elif args[0]=='image': print('sha256:fixture-image')
elif args[0]=='inspect': print('sha256:fixture-image' if '{{.Image}}' in args else 'true')
elif args[0]=='exec':
    if args[2]=='sh':
        if '-s' in args:
            (stack/'preflight-called').write_text('yes')
            if os.environ['RM_FIXTURE_SCENARIO']=='config-incompatible': sys.exit(23)
        elif '/proc/' in args[-1]:
            phases=list((stack/'state/core-updates').glob('*/phase'))
            restored=any(p.read_text().strip()=='rolled-back' for p in phases)
            if not (os.environ['RM_FIXTURE_SCENARIO']=='restored-process-down' and restored):
                print(hashlib.sha256(current).hexdigest()+' /proc/123/exe')
        else: print('/usr/local/bin/xray')
    elif args[2]=='sha256sum': print(hashlib.sha256(current).hexdigest()+' /usr/local/bin/xray')
    elif args[-1]=='version':
        candidate=Path(args[2]).name.startswith('remnamanager-xray-')
        version=('0.0.0' if os.environ['RM_FIXTURE_SCENARIO']=='bad-version' else '26.9.9') if candidate else current_version
        print('Xray '+version+' fixture')
''')
        docker.chmod(0o755)
        script = (repo / "internal/installer/node_core_agent.sh").read_text()
        script = script.replace("/opt/remnanode", stack.as_posix()).replace("/etc/systemd/system", units.as_posix())
        bin_path = (fixture / "bin").as_posix()
        if os.name == "nt":
            bin_path = "/" + bin_path[0].lower() + bin_path[2:]
        script = "export PATH=" + shlex.quote(bin_path) + ':"$PATH"\n' + script
        assets = {"amd64": {"url": "https://github.com/XTLS/Xray-core/releases/download/" + version + "/Xray-linux-64.zip", "sha256": digest}}
        env = dict(os.environ, RM_JOB_ID=job, RM_CORE_ID="b" * 32, RM_CORE_VERSION=version,
                   RM_NODE_IMAGE=image, RM_CORE_ASSETS_B64=base64.b64encode(json.dumps(assets).encode()).decode(),
                   RM_FIXTURE_STACK=stack.as_posix(), RM_FIXTURE_SCENARIO=scenario)

        def run(action):
            result = subprocess.run([args.bash, "-s"], input=script, text=True,
                                    env=dict(env, RM_CORE_ACTION=action), capture_output=True)
            log = (root / "install.log").read_text() if (root / "install.log").exists() else ""
            return result, log

        result, log = run("prepare")
        if scenario in ("bad-checksum", "bad-version", "config-incompatible"):
            assert result.returncode != 0, log
            assert (stack / "compose.json").read_text() == before
            if scenario == "config-incompatible":
                assert "RM_CORE_FAILURE=config-incompatible" in result.stdout
                assert (stack / "preflight-called").exists()
            return
        assert result.returncode == 0 and "RM_CORE_PHASE=prepared" in result.stdout, log
        assert (stack / "preflight-called").exists()
        assert (stack / "compose.json").read_text() == before
        result, log = run("apply")
        assert result.returncode == 0, log
        after = json.loads((stack / "compose.json").read_text())
        node = after["services"]["remnanode"]
        assert node["image"] == image and node["environment"]["SECRET_KEY"] == "preserve-me"
        assert node["volumes"][0]["target"] == "/usr/local/bin/xray"
        assert len(node["volumes"]) == 1 and version in node["volumes"][0]["source"]
        action = "rollback" if scenario in ("rollback", "downgrade-failure", "restored-process-down") else "commit"
        result, log = run(action)
        assert result.returncode == 0, log
        if action == "rollback":
            assert (stack / "compose.json").read_text() == before
            result, log = run("verify-previous")
            if scenario == "restored-process-down":
                assert result.returncode != 0 and "RM_CORE_RUNNING_VERSION=" not in result.stdout, log
                return
            assert result.returncode == 0 and "RM_CORE_RUNNING_VERSION=v" in result.stdout, log
        else:
            assert "RM_CORE_PHASE=committed" in result.stdout


for scenario in ("commit", "rollback", "downgrade", "downgrade-failure", "bad-checksum", "bad-version", "config-incompatible", "restored-process-down"):
    case(scenario)
print("PASS: Xray update and downgrade, failure restores previous core, checksum and version rejection")
