#!/usr/bin/env bash
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
source=${AIOT3588A2_SOURCE_DIR:-"$repo"}
work=${AIOT3588A2_BUILD_DIR:-"$repo/.build/aiot-3588a2"}
kernel=${AIOT3588A2_KERNEL_DIR:-"$work/kernel"}
out=${AIOT3588A2_OUTPUT_DIR:-"$repo/output/aiot-3588a2"}
loader=${AIOT3588A2_UBOOT_DIR:?Set AIOT3588A2_UBOOT_DIR to the matching U-Boot build artifacts}
firmware=${AIOT3588A2_FIRMWARE_DIR:?Set AIOT3588A2_FIRMWARE_DIR to the board firmware directory}
test "$(id -u)" = 0 || { echo 'Run as root to create the recovery rootfs and filesystem image' >&2; exit 1; }
for file in brcm/brcmfmac43456-sdio.bin brcm/brcmfmac43456-sdio.txt; do
    test -s "$firmware/$file"
done
test -s "$loader/u-boot-rockchip.bin" && test -s "$loader/u-boot-initial-env"
grep -q '^aiot3588a2_recovery_part=' "$loader/u-boot-initial-env"
mkdir -p "$work/kernel" "$work/modules" "$out"
export ARCH=arm64 CROSS_COMPILE=${CROSS_COMPILE:-aarch64-linux-gnu-}
export KBUILD_BUILD_USER=builder KBUILD_BUILD_HOST=aiot3588a2-recovery
if [ "${AIOT3588A2_USE_PREBUILT_KERNEL:-0}" != 1 ]; then
    bash "$repo/scripts/build-aiot-3588a2-kernel.sh" "$source" "$kernel"
fi
make -C "$source" O="$kernel" INSTALL_MOD_PATH="$work/modules" INSTALL_MOD_STRIP=1 modules_install
version=$(make -s -C "$source" O="$kernel" kernelrelease)
stage=$(mktemp -d "$work/image.XXXXXX")
root="$stage/rootfs"
boot="$stage/boot"
mkdir -p "$root" "$boot/dtb/rockchip" "$boot/extlinux"
cp -a "$source/rootfs/." "$root/"
if [ "$(uname -m)" != aarch64 ]; then
    install -m 755 "$(command -v qemu-aarch64-static)" "$root/usr/bin/qemu-aarch64-static"
fi
find "$root" -type f -print0 | while IFS= read -r -d '' path; do
    if LC_ALL=C grep -Iq . "$path"; then sed -i 's/\r$//' "$path"; fi
done
rm -f "$root/etc/local.d/g98.start" "$root/usr/sbin/g98-issue" "$root/usr/lib/dhcpcd/dhcpcd-hooks/99-g98-issue"
mkdir -p "$root/dev" "$root/proc" "$root/lib/modules" "$root/lib/firmware"
test -c "$root/dev/null" || mknod -m 666 "$root/dev/null" c 1 3
test -c "$root/dev/urandom" || mknod -m 666 "$root/dev/urandom" c 1 9
cp --remove-destination /etc/resolv.conf "$root/etc/resolv.conf"
chroot "$root" apk add --no-cache e2fsprogs e2fsprogs-extra util-linux gptfdisk iw wpa_supplicant wireless-regdb iproute2 hostapd dnsmasq iptables-legacy
# Restore packaged xtables links that may be missing from the source rootfs.
chroot "$root" apk fix --no-cache --reinstall iptables
cp -a "$work/modules/lib/modules/$version" "$root/lib/modules/"
cp -a "$firmware/." "$root/lib/firmware/"
depmod -b "$root" "$version"
install -m 755 "$repo/board/aiot-3588a2/init" "$root/init"
install -m 755 "$repo/board/aiot-3588a2/aiot3588a2-start" "$root/usr/sbin/aiot3588a2-start"
install -m 755 "$repo/board/aiot-3588a2/aiot3588a2-network" "$root/usr/sbin/aiot3588a2-network"
install -m 755 "$repo/board/aiot-3588a2/aiot3588a2-issue" "$root/usr/sbin/aiot3588a2-issue"
install -m 644 "$repo/board/aiot-3588a2/aiot3588a2-hostapd.conf" "$root/etc/aiot3588a2-hostapd.conf"
install -m 644 "$repo/board/aiot-3588a2/aiot3588a2-dnsmasq.conf" "$root/etc/aiot3588a2-dnsmasq.conf"
install -m 644 "$repo/board/aiot-3588a2/inittab" "$root/etc/inittab"
printf 'aiot-3588a2-recovery\n' > "$root/etc/hostname"
printf '127.0.0.1 localhost aiot-3588a2-recovery\n::1 localhost\n' > "$root/etc/hosts"
printf 'SMDT AIOT-3588A V2.0 Recovery\nWi-Fi: http://192.168.88.1/\nWi-Fi: AIOT-3588A2-Recovery\nSSH / serial login: root / root\n' > "$root/etc/issue"
printf 'SMDT AIOT-3588A V2.0 Recovery\nSSH / serial login: root / root\n' > "$root/etc/motd"
date -u +%s > "$root/etc/aiot3588a2-build-epoch"
printf 'root:root\n' | chroot "$root" /bin/busybox chpasswd
rm -f "$root/etc/ssh"/ssh_host_* "$root/root/.ash_history" "$root/root/.bash_history" "$root/root/.ssh/authorized_keys" "$root/root/.ssh/known_hosts"
chmod 700 "$root/root"
chmod 600 "$root/etc/shadow"
chmod 1777 "$root/tmp" "$root/var/tmp"
chroot "$root" /bin/sh -n /init
chroot "$root" /bin/sh -n /usr/sbin/aiot3588a2-start
chroot "$root" /bin/sh -n /usr/sbin/aiot3588a2-network
chroot "$root" /bin/sh -n /usr/sbin/aiot3588a2-issue
(cd "$repo/rkdev" && go test ./...)
build_time=$(date -u -d "@${SOURCE_DATE_EPOCH:-$(date +%s)}" +%Y.%m.%d)
(cd "$repo/rkdev" && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
    -ldflags="-s -w -X main.buildTime=$build_time" -o "$root/rkdev_arm64" .)
cp "$kernel/arch/arm64/boot/Image" "$boot/Image"
cp "$kernel/arch/arm64/boot/dts/rockchip/rk3588-aiot-3588a2.dtb" "$boot/dtb/rockchip/"
cp "$repo/board/aiot-3588a2/extlinux.conf" "$boot/extlinux/"
rm -f "$root/usr/bin/qemu-aarch64-static"
(cd "$root" && find . -xdev -print0 | cpio --null --quiet -o -H newc --owner=0:0 | pigz -9) > "$boot/initramfs.cpio.gz"
truncate -s 234863616 "$stage/recovery.ext4"
mkfs.ext4 -q -F -L AIOT_RECOVERY -O ^has_journal -d "$boot" "$stage/recovery.ext4"
e2fsck -fn "$stage/recovery.ext4"
image="$stage/aiot-3588a2-recovery.img"
truncate -s 512M "$image"
sgdisk -og -n 1:65536:524253 -t 1:8300 -c 1:AIOT_RECOVERY -n 2:524288:0 -t 2:8300 -c 2:AIOT_SYSTEM "$image"
test "$((32768 + $(stat -c %s "$loader/u-boot-rockchip.bin")))" -le 16777216
dd if="$loader/u-boot-rockchip.bin" of="$image" bs=1M seek=32768 oflag=seek_bytes conv=notrunc status=none
mkenvimage -r -s 0x20000 -o "$stage/default-env.bin" "$loader/u-boot-initial-env"
for offset in 16777216 16908288; do
    dd if="$stage/default-env.bin" of="$image" bs=128K seek="$offset" oflag=seek_bytes conv=notrunc status=none
done
dd if="$stage/recovery.ext4" of="$image" bs=4M seek=8 conv=notrunc status=none
sgdisk -v "$image"
cp "$image" "$out/aiot-3588a2-recovery.img"
cp "$stage/recovery.ext4" "$out/"
cp "$boot/Image" "$boot/initramfs.cpio.gz" "$boot/dtb/rockchip/rk3588-aiot-3588a2.dtb" "$out/"
cp "$kernel/.config" "$out/kernel.config"
printf '%s\n' "$version" > "$out/kernel.release"
(cd "$out" && sha256sum *.img *.ext4 *.dtb Image initramfs.cpio.gz kernel.config > SHA256SUMS)
printf 'Build output: %s\n' "$out"
