# PHX-R3588 recovery

The PHX-R3588 V02 carrier uses a separate `phx-r3588-recovery` branch.
Console: UART2 at 115200 baud. RAM recovery login: root / root.

## Build

Use `scripts/build-phx-r3588.sh` on Linux as root. Requirements include an
AArch64 GNU cross compiler, kernel build dependencies, Go as specified by
`rkdev/go.mod`, qemu-user-static/binfmt for the Alpine rootfs, cpio, pigz,
e2fsprogs, gdisk, kmod and U-Boot host tools.

Set `PHX_UBOOT_DIR` to the matching PHX U-Boot artifacts, including
`u-boot-rockchip.bin` and `initial-env.txt`. Set `PHX_FIRMWARE_DIR` to a
firmware tree containing the board AP6275P firmware/NVRAM/CLM, BCM4362A2
patch and Mali CSF firmware. These binary files are not included in this
board code. Recover them from the factory system with their applicable
licenses. Never substitute another board's calibration NVRAM.

Optional `PHX_BUILD_DIR`, `PHX_OUTPUT_DIR` and `JOBS` control build paths
and parallelism. The script builds and tests the recovery application,
builds the kernel, creates the initramfs and writes a 512 MiB raw disk
image. Artifacts include the recovery filesystem separately for disk
composition and updates.

## Layout and operation

The loader begins at LBA 64. Redundant 128 KiB environments begin at
LBA 32768 and 33024. Partition 1 is `PHX_RECOVERY`, LBA 65536..524253.
The recovery disk image is independently bootable and initially contains
an empty system area. A separate system disk image may include its own
loader and boot/root partitions, without a recovery partition. It can be
flashed directly from LBA 0, or uploaded through this recovery system.

When uploaded to eMMC, the complete system image is written at LBA 524288;
its partitions are projected into the outer GPT with their type, UUID and
attributes preserved. The outer loader, environment and recovery
filesystem are retained. The embedded system loader is inert in this mode.
This keeps the system visible to ordinary U-Boot and Linux partition
readers. The recovery application validates the layout before
accessing the U-Boot environment. Starting the application never writes
the environment or a factory partition automatically.

Do not save this board's environment on the factory partition table:
the environment offsets overlap the factory boot partition. Factory
layout RAM tests require a no-save U-Boot build and this fixed recovery
application.

## Verification boundaries

RAM boots have verified wired Gigabit Ethernet, eMMC reads, GPIO fan and
CAN-FD driver loading, Bluetooth firmware, USB controller/hub enumeration,
Panthor and Rocket registration, and the secure firmware memory reserve.
The Wi-Fi and Bluetooth functions share the RTC's 32768 Hz CLKOUT; each
function explicitly acquires its `lpo` clock. Without these consumers,
the RTC driver disables CLKOUT and Wi-Fi firmware cannot start. A full
system cold boot and dual-band scan have verified the board clock fix.
Display connector function, external boot
media, camera pipelines, physical CAN traffic and audio paths require
separate connected-hardware tests. Driver registration is not a complete
functional test.
