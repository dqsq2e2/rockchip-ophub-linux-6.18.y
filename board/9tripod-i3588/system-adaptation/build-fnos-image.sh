#!/bin/bash
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
board=${I3588_BOARD_DIR:-$(realpath "$here/..")}
base=${I3588_BASE_IMAGE:-"$board/../fnos_Mainland-PE_arm_1.2.0701_nanopc-t6_2882.img"}
out=${I3588_OUTPUT_DIR:-"$board/firmware/fnos"}
work=${I3588_WORK_DIR:-/root/.cache/i3588-fnos-build}
output_image=${I3588_OUTPUT_IMAGE:-"$out/fnos_Mainland-PE_arm_1.2.0701_9tripod-I3588_2882-candidate.img"}
image="$work/system-candidate.img"
boot=${I3588_BOOT_MOUNT:-/root/.cache/i3588-fnos-build-boot}
root=${I3588_ROOT_MOUNT:-/root/.cache/i3588-fnos-build-root}
loader_dir=${I3588_UBOOT_DIR:-"$board/firmware/uboot"}
loader="$loader_dir/u-boot-rockchip.bin"
firmware=${I3588_FIRMWARE_DIR:-"$here/firmware-assets/lib/firmware"}
dtb=${I3588_OS_DTB:-"$here/fnos-dtb/rk3588-9tripod-I3588.dtb"}

test -s "$base" && test -s "$loader" && test -s "$dtb"
test "$((32768 + $(stat -c %s "$loader")))" -le "$((0x1000000))" || { echo 'Loader overlaps environment' >&2; exit 1; }
mkdir -p "$out" "$work" "$boot" "$root"
if [ "${1:-}" != --resume ]; then
test ! -e "$output_image" && test ! -e "$image" || { echo "Refusing to overwrite an existing candidate; use --resume" >&2; exit 1; }
cp --sparse=always "$base" "$image"
test "$(stat -c %s "$base")" = "$(stat -c %s "$image")"

dd if="$loader" of="$image" bs=512 seek=64 conv=notrunc,fsync status=none
mkenvimage -r -s 0x20000 -o "$work/default-env.bin" "$loader_dir/initial-env.txt"
dd if="$work/default-env.bin" of="$image" bs=512 seek=32768 conv=notrunc,fsync status=none
dd if="$work/default-env.bin" of="$image" bs=512 seek=33024 conv=notrunc,fsync status=none
else
    if [ ! -s "$image" ]; then
        cp --sparse=always "$output_image" "$image"
    fi
    test -s "$image"
    dd if="$loader" of="$image" bs=512 seek=64 conv=notrunc,fsync status=none
    mkenvimage -r -s 0x20000 -o "$work/default-env.bin" "$loader_dir/initial-env.txt"
    dd if="$work/default-env.bin" of="$image" bs=512 seek=32768 conv=notrunc,fsync status=none
    dd if="$work/default-env.bin" of="$image" bs=512 seek=33024 conv=notrunc,fsync status=none
fi

cleanup() {
    set +e
    for target in "$root/boot" "$root/proc" "$root/sys" "$root/dev/pts" "$root/dev"; do umount "$target" 2>/dev/null; done
    umount "$root" "$boot" 2>/dev/null
}
trap cleanup EXIT
mount -o loop,rw,offset=33554432,sizelimit=398458880 "$image" "$boot"
mount -o loop,rw,offset=448790528,sizelimit=3402629120 "$image" "$root"
mkdir -p "$root/dev" "$root/dev/pts" "$root/proc" "$root/sys" "$root/boot"
mount --bind "$boot" "$root/boot"
cp --remove-destination /etc/resolv.conf "$root/etc/resolv.conf"
mount --bind /dev "$root/dev"
mount --bind /dev/pts "$root/dev/pts"
mount -t proc proc "$root/proc"
mount -t sysfs sysfs "$root/sys"
mkdir -p "$root/var/log/apt" "$root/var/tmp" "$root/tmp"
chmod 1777 "$root/var/tmp" "$root/tmp"

chroot "$root" apt-get -y --no-install-recommends \
    -o Dpkg::Options::=--force-confold install dkms

for module in gpio-fan rockchip-canfd panthor st-accel hdmirx; do
    # Replace this board's source tree on resume; keep built/installed modules.
    source_target="$root/usr/src/9tripod-I3588-$module-1.0.0"
    case "$source_target" in "$root"/usr/src/9tripod-I3588-*-1.0.0) ;; *) exit 1 ;; esac
    rm -rf -- "$source_target"
    install -d "$source_target"
    tar -C "$here/dkms/$module" --exclude='*.o' --exclude='*.ko' \
        --exclude='*.cmd' --exclude='*.mod' --exclude='*.mod.c' \
        --exclude=Module.symvers --exclude=modules.order --exclude=.tmp_versions \
        -cf - . | tar -C "$source_target" -xf -
    if ! chroot "$root" dkms status -m "9tripod-I3588-$module" -v 1.0.0 | grep -q .; then
        chroot "$root" dkms add -m "9tripod-I3588-$module" -v 1.0.0
    fi
done
version=6.18.18.c1145-trim
optional="$root/lib/modules/$version/updates/trim/alternatives"
if [ -L "$optional" ] && \
    [ "$(readlink "$optional")" = "/usr/lib/modules_trim/$version/alternatives" ] && \
    [ ! -e "$root/usr/lib/modules_trim/$version/alternatives" ]; then
    rm "$optional"
fi
test -f "$root/usr/src/linux-headers-$version/Makefile"
cp "$boot/config-$version" "$root/usr/src/linux-headers-$version/.config"
chroot "$root" dkms autoinstall -k "$version"
for module in 9tripod-I3588-gpio-fan 9tripod-I3588-rockchip-canfd 9tripod-I3588-panthor 9tripod-I3588-st-accel 9tripod-I3588-hdmirx; do
    chroot "$root" dkms status -m "$module" -k "$version" | grep -Fq "$version,"
    chroot "$root" dkms status -m "$module" -k "$version" | grep -Fq ': installed'
done

install -d "$root/usr/share/9tripod-I3588" "$boot/dtb/rockchip" "$root/etc/kernel/header_postinst.d" "$root/etc/modules-load.d"
test -s "$firmware/brcm/brcmfmac43752-pcie.bin"
# Keep the distribution's regulatory database and alternatives intact.
# Install only the board's wireless/BT and GPU firmware additions.
for directory in brcm arm/mali; do
    while IFS= read -r -d '' file; do
        relative=${file#"$firmware/"}
        install -D -m 0644 "$file" "$root/lib/firmware/$relative"
    done < <(find "$firmware/$directory" -type f -print0)
done
install -m 0644 "$dtb" "$root/usr/share/9tripod-I3588/rk3588-9tripod-I3588.dtb"
install -m 0644 "$dtb" "$boot/dtb/rockchip/rk3588-9tripod-I3588.dtb"
install -m 0644 "$here/fnos-rootfs/etc/modules-load.d/9tripod-I3588.conf" "$root/etc/modules-load.d/9tripod-I3588.conf"
for script in 9tripod-I3588-kernel-maintain 9tripod-I3588-kernel-queue 9tripod-I3588-kernel-worker; do
    install -D -m 0755 "$here/fnos-rootfs/usr/local/sbin/$script" "$root/usr/local/sbin/$script"
done
install -D -m 0755 "$here/fnos-rootfs/usr/local/sbin/9tripod-I3588-grow-root" "$root/usr/local/sbin/9tripod-I3588-grow-root"
install -D -m 0755 "$here/fnos-rootfs/usr/trim/bin/resize-rootfs.sh" "$root/usr/trim/bin/resize-rootfs.sh"
install -m 0755 "$here/fnos-rootfs/etc/kernel/header_postinst.d/zz-9tripod-I3588-kernel" "$root/etc/kernel/header_postinst.d/zz-9tripod-I3588-kernel"
# Preserve package hooks with diversions so upgrades cannot restore synchronous
# DKMS builds or switch the boot kernel before its headers are ready.
for hook in postinst.d/15-update-ukernel-ver postinst.d/dkms header_postinst.d/dkms; do
    target="/etc/kernel/$hook"
    if ! chroot "$root" dpkg-divert --list "$target" | grep -q .; then
        mkdir -p "$root/usr/share/9tripod-I3588/vendor-hooks/$(dirname "$hook")"
        chroot "$root" dpkg-divert --local --add --rename --divert "/usr/share/9tripod-I3588/vendor-hooks/$hook" "$target"
    fi
    install -m 0755 "$here/fnos-rootfs/etc/kernel/postinst.d/15-update-ukernel-ver" "$root$target"
done
install -D -m 0644 "$here/fnos-rootfs/etc/systemd/system/9tripod-I3588-kernel-maintain.service" "$root/etc/systemd/system/9tripod-I3588-kernel-maintain.service"
install -D -m 0644 "$here/fnos-rootfs/etc/systemd/system/9tripod-I3588-kernel-maintain.timer" "$root/etc/systemd/system/9tripod-I3588-kernel-maintain.timer"
install -D -m 0644 "$here/fnos-rootfs/etc/systemd/system/nmbd.service.d/foreground.conf" "$root/etc/systemd/system/nmbd.service.d/foreground.conf"
mkdir -p "$root/etc/systemd/system/timers.target.wants"
ln -sfn ../9tripod-I3588-kernel-maintain.timer "$root/etc/systemd/system/timers.target.wants/9tripod-I3588-kernel-maintain.timer"

for assignment in "fdtfile=rockchip/rk3588-9tripod-I3588.dtb" "kernelfile=vmlinuz-$version" "initrdfile=initrd.img-$version"; do
    key=${assignment%%=*}
    if grep -q "^$key=" "$boot/fnEnv.txt"; then
        sed -i "s|^$key=.*|$assignment|" "$boot/fnEnv.txt"
    else
        printf '%s\n' "$assignment" >> "$boot/fnEnv.txt"
    fi
done

for module in i2c-gpio gpio-fan can-dev rockchip_canfd panthor phy-rockchip-usbdp nvme nvme-core uas; do
    grep -qxF "$module" "$root/etc/initramfs-tools/modules" 2>/dev/null || printf '%s\n' "$module" >> "$root/etc/initramfs-tools/modules"
done
# The target kernel builds eMMC/TF, DWC3, XHCI, and USB storage in-tree.
# Keep the initramfs focused on external-storage and DKMS modules.
sed -i 's/^MODULES=.*/MODULES=list/' "$root/etc/initramfs-tools/initramfs.conf"
chroot "$root" depmod -a "$version"
chroot "$root" /usr/local/sbin/9tripod-I3588-kernel-maintain "$version"
test -s "$boot/initrd.img-$version"
printf 'root:root\n' | chroot "$root" /usr/sbin/chpasswd
root_partuuid=$(sgdisk -i 2 "$image" | awk '/Partition unique GUID:/ {print tolower($4)}')
python3 "$here/fix_fnos_boot.py" "$boot" --root-partuuid "$root_partuuid"
mkimage -C none -A arm -T script -d "$boot/boot.cmd" "$boot/boot.scr"
rm -f "$boot/u-boot-rockchip-spi.bin"
bash "$here/clean-fnos-rootfs.sh" "$root"
test -L "$root/etc/systemd/system/multi-user.target.wants/ssh.service"
test -s "$root/etc/ssh/sshd_config.d/10-9tripod-I3588.conf"

cleanup
trap - EXIT
sgdisk -v "$image"
cp --sparse=always "$image" "$output_image.building"
mv -f "$output_image.building" "$output_image"
sha256sum "$output_image" > "$output_image.sha256"
echo "I3588_FNOS_CANDIDATE_BUILT $output_image"
