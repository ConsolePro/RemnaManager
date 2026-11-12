#!/usr/bin/env python3
"""Validate rendered client configurations with a verified official Xray binary."""
import hashlib
import io
import json
import os
import pathlib
import subprocess
import tempfile
import urllib.request
import zipfile


def download(url, limit):
    request = urllib.request.Request(url, headers={"User-Agent": "RemnaManager-template-test"})
    with urllib.request.urlopen(request, timeout=90) as response:
        data = response.read(limit + 1)
    if len(data) > limit:
        raise RuntimeError("Xray release download exceeded its size limit")
    return data


def main():
    tag = "v26.11.9"
    release = json.loads(download("https://api.github.com/repos/XTLS/Xray-core/releases/tags/" + tag, 2 << 20))
    if release["tag_name"] != tag or release["draft"] or release["prerelease"]:
        raise RuntimeError("Unexpected Xray release metadata")
    asset = next(item for item in release["assets"] if item["name"] == "Xray-linux-64.zip")
    expected_url = "https://github.com/XTLS/Xray-core/releases/download/" + tag + "/Xray-linux-64.zip"
    digest = asset.get("digest", "")
    if asset["browser_download_url"] != expected_url or not digest.startswith("sha256:") or len(digest) != 71:
        raise RuntimeError("Official Xray archive has no valid SHA256")
    data = download(expected_url, 128 << 20)
    if hashlib.sha256(data).hexdigest() != digest[7:]:
        raise RuntimeError("Xray archive SHA256 mismatch")
    with tempfile.TemporaryDirectory(prefix="remnamanager-xray-") as directory:
        binary = pathlib.Path(directory) / "xray"
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            member = archive.getinfo("xray")
            if member.file_size > 128 << 20:
                raise RuntimeError("Xray executable exceeded its size limit")
            binary.write_bytes(archive.read(member))
            for name in ("geoip.dat", "geosite.dat"):
                member = archive.getinfo(name)
                if member.file_size > 64 << 20:
                    raise RuntimeError("Xray asset exceeded its size limit")
                (pathlib.Path(directory) / name).write_bytes(archive.read(member))
        if binary.read_bytes()[:4] != b"\x7fELF":
            raise RuntimeError("Xray release is not an ELF executable")
        binary.chmod(0o700)
        environment = dict(os.environ, XRAY_TEST_BINARY=str(binary), XRAY_LOCATION_ASSET=directory)
        subprocess.run(["go", "test", "./internal/routing", "./internal/installer", "-run", "^(TestGeneratedTemplatesWithXray|TestNativeWarpProfileWithXray)$", "-count=1", "-v"], env=environment, check=True, timeout=180)


if __name__ == "__main__":
    main()
