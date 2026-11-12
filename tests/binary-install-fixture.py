"""Exercise install.sh against real Go executables without root or network.

All installation paths and OS/dependency checks are isolated fixture replacements.
The installer still performs its real checksum, executable identity, version,
backup and migration checks. No source compilation is allowed in this fixture.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile

parser = argparse.ArgumentParser()
parser.add_argument("--binary", required=True)
parser.add_argument("--bash", default="bash")
parser.add_argument("--jq", default="jq")
args = parser.parse_args()
repo = Path(__file__).resolve().parents[1]
binary = Path(args.binary).resolve()
version = "v" + (repo / "VERSION").read_text().strip()
payload = binary.read_bytes()
digest = hashlib.sha256(payload).hexdigest()
(repo / ".test-state").mkdir(exist_ok=True)

with tempfile.TemporaryDirectory(prefix="binary-installer-", dir=repo / ".test-state") as temporary:
    fixture = Path(temporary)
    assert fixture.resolve().is_relative_to((repo / ".test-state").resolve())
    lib = fixture / "usr/local/lib/remnamanager"
    commands = fixture / "usr/local/bin"
    config = fixture / "etc/remnamanager"
    mocks = fixture / "mocks"
    for directory in (lib, commands, mocks):
        directory.mkdir(parents=True)
    os_release = fixture / "os-release"
    os_release.write_text('ID=ubuntu\nVERSION_ID="24.04"\n')
    source = fixture / "binary"
    source.write_bytes(payload)
    release = {
        "tag_name": version, "draft": False, "prerelease": False,
        "assets": [
            {"name": "remanager-linux-amd64", "digest": "sha256:" + digest,
             "browser_download_url": f"https://github.com/ConsolePro/RemnaManager/releases/download/{version}/remanager-linux-amd64"},
            {"name": "checksums.txt", "browser_download_url": f"https://github.com/ConsolePro/RemnaManager/releases/download/{version}/checksums.txt"},
        ],
    }
    release_file = fixture / "release.json"
    release_file.write_text(json.dumps(release))
    checksums = fixture / "checksums.txt"
    checksums.write_text(digest + "  remanager-linux-amd64\n")
    requests = fixture / "requests"
    for name in ("apt-get", "dpkg", "dpkg-query", "uname", "flock", "curl", "jq", "go"):
        body = "#!/bin/sh\n"
        if name == "uname":
            body += 'case "$1" in -s) echo Linux;; -m) echo x86_64;; esac\n'
        elif name == "dpkg-query":
            body += "echo 'install ok installed'\n"
        elif name in ("apt-get", "go"):
            body += "echo 'Unexpected compilation or dependency installation' >&2; exit 99\n"
        elif name == "curl":
            body += "url=''; output=''\nwhile [ $# -gt 0 ]; do case \"$1\" in https://*) url=$1;; -o) shift; output=$1;; esac; shift; done\n"
            body += f'printf "%s\\n" "$url" >> {shlex.quote(requests.as_posix())}\n'
            body += 'case "$url" in\n'
            body += f'  https://api.github.com/repos/ConsolePro/RemnaManager/releases/*) cp {shlex.quote(release_file.as_posix())} "$output";;\n'
            body += f'  */remanager-linux-amd64) cp {shlex.quote(source.as_posix())} "$output";;\n'
            body += f'  */checksums.txt) cp {shlex.quote(checksums.as_posix())} "$output";;\n'
            body += '  *) echo "Unexpected download: $url" >&2; exit 98;;\nesac\n'
        elif name == "jq":
            jq = Path(args.jq).as_posix() if args.jq != "jq" else shutil.which("jq")
            assert jq, "jq is required"
            body += "exec " + shlex.quote(jq) + ' "$@"\n'
        else:
            body += "exit 0\n"
        path = mocks / name
        path.write_text(body)
        path.chmod(0o755)
    script = (repo / "install.sh").read_text()
    script = script.replace("/usr/local/lib/remnamanager", lib.as_posix())
    script = script.replace("/usr/local/bin", commands.as_posix())
    script = script.replace("/etc/remnamanager", config.as_posix())
    script = script.replace("/etc/os-release", os_release.as_posix())
    script = script.replace("$EUID == 0", "1 == 1")
    if os.name == "nt":
        # NTFS does not implement the Linux directory permission semantics.
        script = script.replace("install -d -m 0700", "mkdir -p")
    mock_path = mocks.as_posix()
    if os.name == "nt":
        mock_path = "/" + mock_path[0].lower() + mock_path[2:]
    script = "export PATH=" + shlex.quote(mock_path) + ':"$PATH"\n' + script
    panel = commands / "remna"
    panel.write_text("#!/bin/sh\necho panel-cli\n")
    panel.chmod(0o755)
    panel_before = panel.read_bytes()

    def install(*options, succeeds=True):
        result = subprocess.run([args.bash, "-s", "--", *options], input=script,
                                capture_output=True, text=True)
        assert (result.returncode == 0) == succeeds, result.stdout + result.stderr
        if os.name == "nt" and result.returncode == 0:
            # Git Bash emulates ln -s with copies when Windows symlinks are
            # unavailable. Linux CI exercises the actual shortcut checks.
            for name in ("remanager", "re"):
                (commands / name).unlink(missing_ok=True)
        return result

    install()
    installed = lib / "remanager"
    assert installed.read_bytes() == payload
    assert panel.read_bytes() == panel_before
    settings = b'{"language":"ru","initialized":true,"future":42}\n'
    (config / "config.json").write_bytes(settings)
    release["assets"][0]["digest"] = None  # Exercise checksums.txt fallback too.
    release_file.write_text(json.dumps(release))
    install("--version", version)
    assert (config / "config.json").read_bytes() == settings
    assert (lib / "remanager.previous").read_bytes() == payload
    previous = json.loads((lib / "remanager.previous.json").read_text())
    assert previous["Version"] == version and previous["SHA256"] == digest
    # A genuine former Go manager can be recognized without installing Go SDK.
    panel.write_bytes(payload)
    panel.chmod(0o755)
    install()
    assert not panel.exists() and (lib / "remna.previous").read_bytes() == payload
    source.write_bytes(b"corrupted download")
    install(succeeds=False)
    assert installed.read_bytes() == payload
    assert (config / "config.json").read_bytes() == settings
    release["assets"] = []
    release_file.write_text(json.dumps(release))
    install(succeeds=False)
    assert installed.read_bytes() == payload
    downloads = requests.read_text()
    assert "codeload" not in downloads and "go.dev" not in downloads
    assert "/releases/tags/" + version in downloads

print("PASS: binary install and version pinning without Go; checksum fallback; config and CLI preservation; backup; rejection of corrupt/missing assets")
