#!/usr/bin/env bash
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
source=${I3588_SOURCE_DIR:-"$repo"}
work=${I3588_BUILD_DIR:-"$repo/.build/9tripod-i3588"}
out=${I3588_OUTPUT_DIR:-"$repo/output/9tripod-i3588"}
loader=${I3588_UBOOT_DIR:?Set I3588_UBOOT_DIR to the matching U-Boot build artifacts}
firmware=${I3588_FIRMWARE_DIR:?Set I3588_FIRMWARE_DIR to the board firmware directory}
test "$(id -u)" = 0 || { echo 'Run as root to create the recovery rootfs and filesystem image' >&2; exit 1; }
for file in brcm/brcmfmac43752-pcie.bin brcm/brcmfmac43752-pcie.txt brcm/brcmfmac43752-pcie.clm_blob brcm/BCM4362A2.hcd arm/mali/arch10.8/mali_csffw.bin; do
    test -s "$firmware/$file"
done
test -s "$loader/u-boot-rockchip.bin" && test -s "$loader/initial-env.txt"
grep -q '^i3588_recovery_part=' "$loader/initial-env.txt"
mkdir -p "$work/kernel" "$work/modules" "$out"
export ARCH=arm64 CROSS_COMPILE=${CROSS_COMPILE:-aarch64-linux-gnu-}
export KBUILD_BUILD_USER=builder KBUILD_BUILD_HOST=i3588-recovery
make -C "$source" O="$work/kernel" i3588_recovery_defconfig
make -C "$source" O="$work/kernel" -j"${JOBS:-$(nproc)}" Image modules rockchip/rk3588-9tripod-I3588.dtb
make -C "$source" O="$work/kernel" INSTALL_MOD_PATH="$work/modules" modules_install
version=$(make -s -C "$source" O="$work/kernel" kernelrelease)
stage=$(mktemp -d "$work/image.XXXXXX")
root="$stage/rootfs"
boot="$stage/boot"
mkdir -p "$root" "$boot/dtb/rockchip" "$boot/extlinux"
cp -a "$repo/rootfs/." "$root/"
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
install -m 755 "$repo/board/9tripod-i3588/init" "$root/init"
install -m 755 "$repo/board/9tripod-i3588/i3588-start" "$root/usr/sbin/i3588-start"
install -m 755 "$repo/board/9tripod-i3588/i3588-network" "$root/usr/sbin/i3588-network"
install -m 755 "$repo/board/9tripod-i3588/i3588-issue" "$root/usr/sbin/i3588-issue"
install -m 644 "$repo/board/9tripod-i3588/i3588-hostapd.conf" "$root/etc/i3588-hostapd.conf"
install -m 644 "$repo/board/9tripod-i3588/i3588-dnsmasq.conf" "$root/etc/i3588-dnsmasq.conf"
install -m 644 "$repo/board/9tripod-i3588/inittab" "$root/etc/inittab"
printf '9tripod-i3588-recovery\n' > "$root/etc/hostname"
printf '127.0.0.1 localhost 9tripod-i3588-recovery\n::1 localhost\n' > "$root/etc/hosts"
printf '9Tripod I3588 Recovery\nLAN / Wi-Fi: http://192.168.88.1/\nWi-Fi: I3588-Recovery    Password: I3588recovery\nSSH / serial login: root / root\n' > "$root/etc/issue"
printf '9Tripod I3588 Recovery\nSSH / serial login: root / root\n' > "$root/etc/motd"
date -u +%s > "$root/etc/i3588-build-epoch"
printf 'root:root\n' | chroot "$root" /bin/busybox chpasswd
rm -f "$root/etc/ssh"/ssh_host_* "$root/root/.ash_history" "$root/root/.bash_history" "$root/root/.ssh/authorized_keys" "$root/root/.ssh/known_hosts"
chmod 700 "$root/root"
chmod 600 "$root/etc/shadow"
chmod 1777 "$root/tmp" "$root/var/tmp"
chroot "$root" /bin/sh -n /init
chroot "$root" /bin/sh -n /usr/sbin/i3588-start
chroot "$root" /bin/sh -n /usr/sbin/i3588-network
chroot "$root" /bin/sh -n /usr/sbin/i3588-issue
(cd "$repo/rkdev" && go test ./...)
bash "$repo/scripts/build-i3588-app.sh" "$root/rkdev_arm64"
cp "$work/kernel/arch/arm64/boot/Image" "$boot/Image"
cp "$work/kernel/arch/arm64/boot/dts/rockchip/rk3588-9tripod-I3588.dtb" "$boot/dtb/rockchip/"
cp "$repo/board/9tripod-i3588/extlinux.conf" "$boot/extlinux/"
(cd "$root" && find . -xdev -print0 | cpio --null --quiet -o -H newc --owner=0:0 | pigz -9) > "$boot/initramfs.cpio.gz"
truncate -s 234863616 "$stage/recovery.ext4"
mkfs.ext4 -q -F -L I3588_RECOVERY -O ^has_journal -d "$boot" "$stage/recovery.ext4"
e2fsck -fn "$stage/recovery.ext4"
image="$stage/9tripod-i3588-recovery.img"
truncate -s 512M "$image"
sgdisk -og -n 1:65536:524253 -t 1:8300 -c 1:I3588_RECOVERY -n 2:524288:0 -t 2:8300 -c 2:I3588_SYSTEM "$image"
test "$((32768 + $(stat -c %s "$loader/u-boot-rockchip.bin")))" -le 16777216
dd if="$loader/u-boot-rockchip.bin" of="$image" bs=1M seek=32768 oflag=seek_bytes conv=notrunc status=none
mkenvimage -r -s 0x20000 -o "$stage/default-env.bin" "$loader/initial-env.txt"
for offset in 16777216 16908288; do
    dd if="$stage/default-env.bin" of="$image" bs=128K seek="$offset" oflag=seek_bytes conv=notrunc status=none
done
dd if="$stage/recovery.ext4" of="$image" bs=4M seek=8 conv=notrunc status=none
sgdisk -v "$image"
cp "$image" "$out/9tripod-i3588-recovery.img"
cp "$stage/recovery.ext4" "$out/"
cp "$boot/Image" "$boot/initramfs.cpio.gz" "$boot/dtb/rockchip/rk3588-9tripod-I3588.dtb" "$out/"
cp "$work/kernel/.config" "$out/kernel.config"
printf '%s\n' "$version" > "$out/kernel.release"
(cd "$out" && sha256sum *.img *.ext4 *.dtb Image initramfs.cpio.gz kernel.config > SHA256SUMS)
printf 'Build output: %s\n' "$out"
