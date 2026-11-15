"""Exercise update scripts in disposable chroots and real POSIX lock processes."""
import fcntl
import os
from pathlib import Path
import re
import runpy
import shutil
import subprocess
import tempfile
import unittest

SOURCE = Path(__file__).resolve().parent / "fnos-rootfs/usr/local/sbin"
VERSION = "6.18.99-i3588-test"


def put(root, name, text, mode=0o644):
    target = root / name.lstrip("/")
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(text)
    target.chmod(mode)
    return target


class MaintainIntegration(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="i3588-update-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        copied = set()

        def binary(name):
            source = Path(shutil.which(name))
            target = self.root / source.relative_to("/")
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source, target)
            for library in re.findall(r"(/[^\s()]+)", subprocess.check_output(["ldd", str(source)], text=True)):
                if library in copied:
                    continue
                copied.add(library)
                destination = self.root / library.lstrip("/")
                destination.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(library, destination)

        for command in ("sh", "mkdir", "touch", "flock", "grep", "cp", "sed", "tail", "install", "mktemp", "chmod", "sync", "mv", "rm", "dpkg"):
            binary(command)
        (self.root / "bin").mkdir(exist_ok=True)
        (self.root / "bin/sh").symlink_to("/usr/bin/sh")
        (self.root / "dev").mkdir()
        os.mknod(self.root / "dev/null", 0o666 | 0o20000, os.makedev(1, 3))
        put(self.root, "/usr/local/sbin/9tripod-I3588-kernel-maintain", (SOURCE / "9tripod-I3588-kernel-maintain").read_text(), 0o755)
        put(self.root, "/boot/fnEnv.txt", "rootdev=UUID=original\nkernelfile=vmlinuz-6.18.18\ninitrdfile=initrd.img-6.18.18\nfdtfile=original.dtb\n")
        self.original = (self.root / "boot/fnEnv.txt").read_bytes()
        put(self.root, "/boot/vmlinuz-" + VERSION, "test kernel")
        put(self.root, "/boot/config-" + VERSION, "CONFIG_DRM_SCHED=m\n")
        put(self.root, "/usr/share/9tripod-I3588/rk3588-9tripod-I3588.dtb", "test dtb")
        put(self.root, "/usr/bin/dkms", '''#!/bin/sh
echo "dkms $*" >> /events
if [ "$1" = autoinstall ]; then
    test ! -f /fail-dkms
else
    test ! -f /missing-installed || exit 0
    echo "test/1.0.0, 6.18.99-i3588-test, aarch64: installed"
fi
''', 0o755)
        put(self.root, "/usr/bin/depmod", '#!/bin/sh\necho "depmod $*" >> /events\n', 0o755)
        put(self.root, "/usr/bin/update-initramfs", '''#!/bin/sh
echo "initramfs $*" >> /events
test ! -f /fail-initramfs || exit 1
echo initramfs > /boot/initrd.img-6.18.99-i3588-test
''', 0o755)

    def headers(self):
        for item in ("Makefile", "Module.symvers", "include/generated/autoconf.h", "include/config/auto.conf"):
            put(self.root, "/lib/modules/" + VERSION + "/build/" + item, "prepared test header\n")

    def run_update(self):
        return subprocess.run([shutil.which("chroot"), str(self.root), "/usr/local/sbin/9tripod-I3588-kernel-maintain", VERSION],
                              env={"PATH": "/usr/bin:/bin"}, capture_output=True, text=True)

    def unchanged(self):
        self.assertEqual(self.original, (self.root / "boot/fnEnv.txt").read_bytes())
        self.assertTrue((self.root / ("var/lib/9tripod-I3588-kernel/pending/" + VERSION)).exists())

    def test_headers_not_ready_do_not_invoke_dkms(self):
        result = self.run_update()
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertFalse((self.root / "events").exists())
        self.unchanged()

    def test_dkms_failure_preserves_boot(self):
        self.headers()
        put(self.root, "/fail-dkms", "fail")
        self.assertNotEqual(0, self.run_update().returncode)
        self.unchanged()
        self.assertNotIn("initramfs", (self.root / "events").read_text())

    def test_missing_installed_module_preserves_boot(self):
        self.headers()
        put(self.root, "/missing-installed", "missing")
        self.assertNotEqual(0, self.run_update().returncode)
        self.unchanged()

    def test_initramfs_failure_preserves_boot(self):
        self.headers()
        put(self.root, "/fail-initramfs", "fail")
        self.assertNotEqual(0, self.run_update().returncode)
        self.unchanged()

    def test_success_commits_after_build_and_initramfs(self):
        self.headers()
        result = self.run_update()
        self.assertEqual(0, result.returncode, result.stderr)
        events = (self.root / "events").read_text()
        self.assertLess(events.index("dkms autoinstall"), events.index("depmod"))
        self.assertLess(events.index("depmod"), events.index("initramfs"))
        env = (self.root / "boot/fnEnv.txt").read_text()
        self.assertIn("kernelfile=vmlinuz-" + VERSION, env)
        self.assertIn("initrdfile=initrd.img-" + VERSION, env)
        self.assertIn("rootdev=UUID=original", env)
        self.assertFalse((self.root / ("var/lib/9tripod-I3588-kernel/pending/" + VERSION)).exists())

    def test_in_tree_drivers_do_not_require_dkms_install_status(self):
        self.headers()
        put(self.root, "/boot/config-" + VERSION, "CONFIG_SENSORS_GPIO_FAN=y\nCONFIG_CAN_ROCKCHIP_CANFD=m\nCONFIG_DRM_PANTHOR=m\nCONFIG_IIO_ST_ACCEL_I2C_3AXIS=m\nCONFIG_VIDEO_SYNOPSYS_HDMIRX=m\n")
        put(self.root, "/missing-installed", "missing")
        result = self.run_update()
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertNotIn("dkms status", (self.root / "events").read_text())


class WorkerLocks(unittest.TestCase):
    def test_posix_dpkg_lock_excludes_worker_and_persists_through_child(self):
        worker = runpy.run_path(str(SOURCE / "9tripod-I3588-kernel-worker"))["run_pending"]
        with tempfile.TemporaryDirectory(prefix="i3588-lock-test-") as temporary:
            root = Path(temporary)
            pending, locks = root / "pending", root / "locks"
            pending.mkdir()
            locks.mkdir()
            (pending / VERSION).touch()
            helper = put(root, "/maintain", '''#!/usr/bin/python3
import fcntl, pathlib, sys
root = pathlib.Path(__file__).parent
for name in ("lock-frontend", "lock"):
    with (root / "locks" / name).open("a") as handle:
        try:
            fcntl.lockf(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            continue
        raise SystemExit("dpkg lock was released during maintenance")
(root / "ran").touch()
(root / "pending" / sys.argv[1]).unlink()
''', 0o755)
            holder = subprocess.Popen(["python3", "-c", "import fcntl,sys; f=open(sys.argv[1],'a'); fcntl.lockf(f,fcntl.LOCK_EX); print('locked',flush=True); sys.stdin.read()", str(locks / "lock")], stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
            try:
                self.assertEqual("locked", holder.stdout.readline().strip())
                self.assertEqual(0, worker(pending, locks, str(helper)))
                self.assertFalse((root / "ran").exists())
            finally:
                holder.communicate("")
            self.assertEqual(0, worker(pending, locks, str(helper)))
            self.assertTrue((root / "ran").exists())
            self.assertFalse((pending / VERSION).exists())


if __name__ == "__main__":
    unittest.main(verbosity=2)
