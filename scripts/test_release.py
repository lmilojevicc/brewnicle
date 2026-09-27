import io
from pathlib import Path
import struct
import tarfile
import tempfile
import unittest

import release


def executable(target):
    data = bytearray(32)
    if target.startswith("linux_"):
        data[:6] = b"\x7fELF\x02\x01"
        struct.pack_into("<H", data, 18, 62 if target.endswith("amd64") else 183)
    else:
        data[:4] = b"\xcf\xfa\xed\xfe"
        struct.pack_into("<I", data, 4, 0x01000007 if target.endswith("amd64") else 0x0100000C)
    return bytes(data)


class ReleaseTests(unittest.TestCase):
    def test_dependency_license_basename(self):
        for name in ("LICENSE", "COPYING", "LICENCE.txt"):
            self.assertIsNotNone(release.LICENSE_NAME.fullmatch(name))
        for name in ("SOURCE_NOTICES.txt", "LICENSED", "fooLICENSE", "PATENTS"):
            self.assertIsNone(release.LICENSE_NAME.fullmatch(name))

    def test_strict_tags_and_names(self):
        self.assertEqual("brewnicle_v1.2.3_linux_arm64.tar.gz", release.archive_name("v1.2.3", "linux_arm64"))
        for tag in ("1.2.3", "v01.2.3", "v1.2.3-rc.1", "v1.2.3+build", "v1.2.3\n", "v١.2.3", "v1.2.3/../bad"):
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                release.archive_name(tag, "linux_arm64")
        with self.assertRaises(ValueError):
            release.archive_name("v1.2.3", "windows_amd64")

    def test_executable_formats(self):
        for target in release.TARGETS:
            release.check_binary(executable(target), target)
            with self.assertRaises(ValueError):
                release.check_binary(b"not a binary", target)
            for other in release.TARGETS:
                if other != target:
                    with self.assertRaises(ValueError):
                        release.check_binary(executable(other), target)

    def make_archive(self, target, omit=None, extra=None, executable_mode=0o755):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        path = Path(tmp.name) / "archive.tar.gz"
        files = {"brewnicle": executable(target), "LICENSE": b"MIT", "licenses/go/LICENSE": b"BSD",
                 "licenses/MODULES.txt": b"example.org/module@v1.0.0\n",
                 "licenses/example.org/module@v1.0.0/LICENSE": b"MIT",
                 "licenses/supplemental/Unicode-3.0.txt": b"Unicode",
                 "licenses/supplemental/Unicode-DFS-2016.txt": b"Unicode",
                 "licenses/supplemental/Unicode-15.0.0-NOTICE.txt": b"Unicode",
                 "licenses/supplemental/Unicode-17.0.0-NOTICE.txt": b"Unicode",
                 "licenses/supplemental/HSLuv-LICENSE.txt": b"MIT"}
        if omit:
            files.pop(omit)
        with tarfile.open(path, "w:gz") as tar:
            for name, data in files.items():
                member = tarfile.TarInfo(name)
                member.size = len(data)
                member.mode = executable_mode if name == "brewnicle" else 0o644
                tar.addfile(member, io.BytesIO(data))
            if extra:
                tar.addfile(extra, io.BytesIO(b"x" * extra.size))
        return path

    def test_archive_contract(self):
        for target in release.TARGETS:
            release.verify_archive(self.make_archive(target), target)

    def test_missing_notices_and_binary(self):
        for name in ("brewnicle", "LICENSE", "licenses/go/LICENSE", "licenses/MODULES.txt",
                     "licenses/example.org/module@v1.0.0/LICENSE"):
            with self.subTest(name=name), self.assertRaises(ValueError):
                release.verify_archive(self.make_archive("linux_arm64", omit=name), "linux_arm64")

    def test_unsafe_and_duplicate_members(self):
        for name in ("../escape", "/absolute", "licenses/../escape", "licenses//bad", "brewnicle", "unrelated"):
            info = tarfile.TarInfo(name)
            info.size = 1
            with self.subTest(name=name), self.assertRaises(ValueError):
                release.verify_archive(self.make_archive("linux_arm64", extra=info), "linux_arm64")
        info = tarfile.TarInfo("licenses/link")
        info.type = tarfile.SYMTYPE
        info.linkname = "../../escape"
        with self.assertRaises(ValueError):
            release.verify_archive(self.make_archive("linux_arm64", extra=info), "linux_arm64")

    def test_archive_requires_executable(self):
        with self.assertRaises(ValueError):
            release.verify_archive(self.make_archive("linux_arm64", executable_mode=0o644), "linux_arm64")


if __name__ == "__main__":
    unittest.main()
