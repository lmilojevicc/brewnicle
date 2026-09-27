#!/usr/bin/env python3
"""Build and verify the four supported archives; never publish a release."""

import argparse
import gzip
import hashlib
import os
from pathlib import Path
import re
import shutil
import struct
import subprocess
import tarfile
import tempfile

TARGETS = ("darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64")
TAG = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", re.ASCII)
LICENSE_NAME = re.compile(r"(?:LICEN[CS]E|COPYING)(?:[._-].*)?", re.IGNORECASE)


def archive_name(tag, target):
    if not TAG.fullmatch(tag) or target not in TARGETS:
        raise ValueError("expected stable vMAJOR.MINOR.PATCH and supported target")
    return f"brewnicle_{tag}_{target}.tar.gz"


def check_binary(data, target):
    if target.startswith("linux_"):
        machine = 62 if target.endswith("amd64") else 183
        valid = len(data) >= 20 and data[:6] == b"\x7fELF\x02\x01"
        valid = valid and struct.unpack_from("<H", data, 18)[0] == machine
    else:
        cpu = 0x01000007 if target.endswith("amd64") else 0x0100000C
        valid = len(data) >= 8 and data[:4] == b"\xcf\xfa\xed\xfe"
        valid = valid and struct.unpack_from("<I", data, 4)[0] == cpu
    if not valid:
        raise ValueError(f"wrong executable format/architecture: {target}")


def verify_archive(path, target):
    with tarfile.open(path, "r:gz") as archive:
        members = archive.getmembers()
        names = [m.name for m in members]
        if len(names) != len(set(names)):
            raise ValueError("duplicate archive member")
        for member in members:
            parts = member.name.split("/")
            if not member.isfile() or "\\" in member.name or any(p in ("", ".", "..") for p in parts):
                raise ValueError("unsafe archive member")
            if member.name not in ("brewnicle", "LICENSE") and not member.name.startswith("licenses/"):
                raise ValueError("unexpected archive member")
        for required in ("brewnicle", "LICENSE", "licenses/MODULES.txt", "licenses/go/LICENSE",
                         "licenses/supplemental/Unicode-3.0.txt", "licenses/supplemental/Unicode-DFS-2016.txt",
                         "licenses/supplemental/Unicode-15.0.0-NOTICE.txt", "licenses/supplemental/Unicode-17.0.0-NOTICE.txt",
                         "licenses/supplemental/HSLuv-LICENSE.txt"):
            if required not in names or archive.getmember(required).size == 0:
                raise ValueError(f"missing/empty {required}")
        if not archive.getmember("brewnicle").mode & 0o111:
            raise ValueError("binary is not executable")
        check_binary(archive.extractfile("brewnicle").read(32), target)
        modules = archive.extractfile("licenses/MODULES.txt").read().decode().splitlines()
        license_files = [name for name in names if LICENSE_NAME.fullmatch(name.rsplit("/", 1)[-1])]
        if not modules or any(not any(name.startswith(f"licenses/{module}/") for name in license_files)
                              for module in modules):
            raise ValueError("missing dependency licenses")


def build(tag, output):
    archive_name(tag, TARGETS[0])
    output = output.resolve()
    # Refuse reuse so stale artifacts cannot be published under a new tag.
    output.mkdir(parents=True, exist_ok=False)
    env = dict(os.environ, CGO_ENABLED="0", GOWORK="off")
    subprocess.run(["go", "mod", "download"], env=env, check=True)
    subprocess.run(["go", "mod", "verify"], env=env, check=True)
    with tempfile.TemporaryDirectory(prefix="brewnicle-release-") as tmp:
        stage = Path(tmp)
        shutil.copyfile("LICENSE", stage / "LICENSE")
        subprocess.run(["go", "run", "./scripts/notices", str(stage / "licenses")], env=env, check=True)
        for target in TARGETS:
            goos, goarch = target.split("_")
            subprocess.run(["go", "build", "-mod=readonly", "-trimpath", "-buildvcs=false",
                            "-o", str(stage / "brewnicle"), "./cmd/brewnicle"],
                           env=dict(env, GOOS=goos, GOARCH=goarch), check=True)
            path = output / archive_name(tag, target)
            with path.open("wb") as raw, gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as gz:
                with tarfile.open(fileobj=gz, mode="w") as archive:
                    for file in sorted(stage.rglob("*")):
                        if not file.is_file():
                            continue
                        info = archive.gettarinfo(file, arcname=file.relative_to(stage).as_posix())
                        info.uid = info.gid = info.mtime = 0
                        info.uname = info.gname = ""
                        info.mode = 0o755 if info.name == "brewnicle" else 0o644
                        with file.open("rb") as source:
                            archive.addfile(info, source)
            verify_archive(path, target)
    sums = "".join(f"{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.name}\n"
                   for p in sorted(output.glob("*.tar.gz")))
    (output / "SHA256SUMS").write_text(sums)
    print(f"Verified four archives and SHA256SUMS in {output}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("tag", help="stable vMAJOR.MINOR.PATCH (local smoke builds do not publish)")
    parser.add_argument("output", type=Path, help="new output directory")
    args = parser.parse_args()
    build(args.tag, args.output)
