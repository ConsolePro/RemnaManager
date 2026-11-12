"""Run the agent's real /proc discovery against real processes and rw-core aliases.

Only Docker transport is mocked. No Xray config, tokens or network are needed.
"""
import os
from pathlib import Path
import json
import shutil
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[1]
agent = (repo / "internal/installer/node_warp_agent.sh").read_text()
start = agent.index("running_xray() {")
function = agent[start:agent.index("\n}\n", start) + 3]

with tempfile.TemporaryDirectory(prefix="warp-process-") as temporary:
    root = Path(temporary)
    binary = root / "engine"
    real_core=os.environ.get("XRAY_TEST_BINARY")
    shutil.copyfile(real_core or "/bin/sleep", binary)
    binary.chmod(0o700)
    legacy = root / "xray"
    modern = root / "rw-core"
    mock = root / "docker"
    mock.write_text('#!/bin/sh\n[ "$1" = exec ] || exit 2\nshift 2\nexec "$@"\n')
    mock.chmod(0o700)
    function = function.replace("/usr/local/bin/rw-core", str(modern)).replace("/usr/local/bin/xray", str(legacy))
    script = 'set -eu\ncompose() { printf "%s\\n" "$TEST_CONTAINER"; }\n' + function + '\nrunning_xray\n'
    environment = dict(os.environ, PATH=str(root) + os.pathsep + os.environ["PATH"], TEST_CONTAINER="first-container")
    config=root/'config.json'
    config.write_text(json.dumps({'log':{'loglevel':'none'},'outbounds':[{'protocol':'freedom'}]}))

    def probe(check=True):
        result = subprocess.run(["bash"], input=script, text=True, capture_output=True, env=environment, timeout=10)
        if check:
            assert result.returncode == 0, result.stderr
            return result.stdout.strip()
        assert result.returncode != 0, "dead or unrelated process was accepted as Xray"
        return result.returncode

    assert probe(False)==2, 'missing executable was confused with an absent process'
    for alias in (legacy, modern):
        alias.symlink_to(binary)
        command=[str(alias),'run','-config',str(config)] if real_core else [str(alias),'60']
        process = subprocess.Popen(command,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        try:
            fingerprint = probe()
            assert fingerprint.startswith("first-container:" + str(process.pid) + ":"), fingerprint
            environment["TEST_CONTAINER"] = "second-container"
            assert probe() != fingerprint, "container replacement reused the previous identity"
            environment["TEST_CONTAINER"] = "first-container"
            if alias == modern:
                assert Path('/proc/' + str(process.pid) + '/comm').read_text().strip() == 'rw-core'
        finally:
            process.terminate()
            process.wait(timeout=10)
        assert probe(False)==3, 'stopped process was not reported as confirmed absent'
        alias.unlink()
    print("WARP process discovery: xray and rw-core aliases recognized; stopped processes rejected; container identity checked")
