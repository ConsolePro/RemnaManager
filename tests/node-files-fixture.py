"""Exercise website changes and layout migration in an isolated filesystem.

Run: python tests/node-files-fixture.py --bash /path/to/bash
Systemd, nginx, root checks and locks are replaced with fixture commands.
"""
import argparse
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import io
import base64
import shlex

parser = argparse.ArgumentParser()
parser.add_argument("--bash", default="bash")
args = parser.parse_args()
repo = Path(__file__).resolve().parents[1]
job = "a" * 32


def run_case(migration=False, fail=False, resume=False):
    (repo / ".test-state").mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="node-files-", dir=repo / ".test-state") as temporary:
        fixture = Path(temporary)
        assert fixture.resolve().is_relative_to((repo / ".test-state").resolve())
        stack = fixture / "opt/remnanode"
        old_root = fixture / "var/lib/remnamanager-node"
        old_web = fixture / "var/www/remnamanager-node"
        root = old_root if migration else stack / "state"
        web = old_web if migration else stack / "www"
        units = fixture / "etc/systemd/system"
        link = fixture / "etc/nginx/conf.d/remnamanager-node.conf"
        for directory in (root, web / "site", units, link.parent, fixture / "bin"):
            directory.mkdir(parents=True, exist_ok=True)
        (root / "job-id").write_text(job)
        (root / "ready").write_text(job)
        (web / "site/index.html").write_text("original")
        stack.mkdir(parents=True, exist_ok=True)
        (stack / ".remnamanager-go-node").write_text(job)
        log = fixture / "calls"
        for command in ("systemctl", "systemd-run", "flock", "nginx", "id"):
            body = "#!/bin/sh\n"
            if command == "id":
                body += "echo 0\n"
            else:
                body += f'echo "{command} $*" >> "{log.as_posix()}"\n'
                if command == "nginx" and fail:
                    body += "exit 1\n"
            path = fixture / "bin" / command
            path.write_text(body)
            path.chmod(0o755)
        env = dict(os.environ)

        def run(source, **variables):
            script = (repo / "internal/installer" / source).read_text()
            if os.name == "nt" and source == "node_layout_agent.sh":
                # Windows cannot rename a directory containing an open handle.
                # Locking is a Linux integration concern, outside this fixture.
                script = script.replace('exec 9>"$base/agent.lock"', ":")
            bin_path = (fixture / "bin").as_posix()
            if os.name == "nt":
                bin_path = "/" + bin_path[0].lower() + bin_path[2:]
            script = "export PATH=" + shlex.quote(bin_path) + ':"$PATH"\n' + script
            for before, after in (
                ("/opt/remnanode", stack),
                ("/var/lib/remnamanager-node", old_root),
                ("/var/www/remnamanager-node", old_web),
                ("/etc/nginx/conf.d/remnamanager-node.conf", link),
                ("/etc/systemd/system", units),
            ):
                script = script.replace(before, after.as_posix())
            result = subprocess.run(
                [args.bash, "-s"], input=script, text=True,
                env=dict(env, RM_JOB_ID=job, **variables), capture_output=True,
            )
            return result

        if migration:
            (root / "acme").mkdir()
            (root / "acme/domain.conf").write_text(f"{old_root.as_posix()}/ssl\n{old_web.as_posix()}/acme\n")
            link.write_text(f"# Managed by RemnaManager node installer\nroot {old_web.as_posix()}/site;\ncert {old_root.as_posix()}/ssl;\n")
            if resume:
                (root / "layout-pending").write_text(job)
                root.rename(stack / "state")
            result = run("node_layout_agent.sh")
            if fail:
                assert result.returncode != 0
                assert old_root.is_dir() and old_web.is_dir(), result.stderr
                assert not link.is_symlink() and old_web.as_posix() in link.read_text()
            else:
                assert result.returncode == 0, result.stderr
                assert "RM_NODE_LAYOUT=1" in result.stdout
                assert not old_root.exists() and not old_web.exists()
                config = (stack / "state/acme/domain.conf").read_text()
                assert (stack / "state").as_posix() in config
                assert (stack / "www").as_posix() in config
                assert link.exists()
                assert run("node_layout_agent.sh").returncode == 0
            return

        def archive(content):
            buffer = io.BytesIO()
            with tarfile.open(fileobj=buffer, mode="w:gz") as tar:
                data = content.encode()
                header = tarfile.TarInfo("index.html")
                header.size = len(data)
                header.mode = 0o644
                tar.addfile(header, io.BytesIO(data))
            return base64.b64encode(buffer.getvalue()).decode()

        def change(action, content="changed"):
            result = run("node_site_agent.sh", RM_SITE_ACTION=action,
                         RM_SITE_NAME="atlas", RM_SITE_B64=archive(content))
            assert result.returncode == 0, result.stderr
            return result.stdout

        change("apply")
        assert (web / "site/index.html").read_text() == "changed"
        change("rollback")
        assert (web / "site/index.html").read_text() == "original"
        change("apply", "second")
        change("commit")
        assert (web / "site/index.html").read_text() == "second"
        change("apply", "third")
        change("commit")
        assert (web / "site/index.html").read_text() == "third"
        backups = list((root / "site-change").glob("previous.*/index.html"))
        assert len(backups) == 2


run_case()
run_case(migration=True)
run_case(migration=True, fail=True)
run_case(migration=True, resume=True)
print("PASS: website change, rollback, repeated commit; layout migration, rollback, resume")
