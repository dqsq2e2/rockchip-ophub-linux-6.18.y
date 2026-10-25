#!/bin/bash

set -ex

WORKDIR=$(pwd)
TIMESTAMP=$(date +%Y%m%d)

# update rkdev
cd ${WORKDIR}/rkdev
./kdev-build.sh
cp -a rkdev_arm64 ${WORKDIR}/rootfs/
sync

# build kernel
cd ${WORKDIR}/
#make ARCH=arm64 \
#  CROSS_COMPILE=aarch64-linux-gnu- \
#  KBUILD_BUILD_USER="builder" \
#  KBUILD_BUILD_HOST="kdevbuilder" \
#  LOCALVERSION=-kdev \
#  mrproper

# build kernel Image
make ARCH=arm64 \
  CROSS_COMPILE=aarch64-linux-gnu- \
  KBUILD_BUILD_USER="builder" \
  KBUILD_BUILD_HOST="kdevbuilder" \
  LOCALVERSION=-kdev \
  aigo_s60_defconfig

# check kver
KVER=$(make LOCALVERSION=-kdev kernelrelease)
KVER="${KVER/kdev*/kdev}"
if [[ "$KVER" != *kdev ]]; then
  echo "ERROR: KVER does not end with 'kdev'"
  exit 1
fi
echo "KVER: ${KVER}"

make ARCH=arm64 \
  CROSS_COMPILE=aarch64-linux-gnu- \
  KBUILD_BUILD_USER="builder" \
  KBUILD_BUILD_HOST="kdevbuilder" \
  LOCALVERSION=-kdev \
  rockchip/rk3588-aigo-s60.dtb \
  -j$(nproc)

make ARCH=arm64 \
  CROSS_COMPILE=aarch64-linux-gnu- \
  KBUILD_BUILD_USER="builder" \
  KBUILD_BUILD_HOST="kdevbuilder" \
  LOCALVERSION=-kdev \
  KCFLAGS="-Wno-unused-function" \
  -j$(nproc)

make ARCH=arm64 \
  CROSS_COMPILE=aarch64-linux-gnu- \
  KBUILD_BUILD_USER="builder" \
  KBUILD_BUILD_HOST="kdevbuilder" \
  LOCALVERSION=-kdev \
  KCFLAGS="-Wno-unused-function" \
  modules -j$(nproc)

make ARCH=arm64 \
  CROSS_COMPILE=aarch64-linux-gnu- \
  KBUILD_BUILD_USER="builder" \
  KBUILD_BUILD_HOST="kdevbuilder" \
  LOCALVERSION=-kdev \
  INSTALL_MOD_PATH=$(pwd)/kos \
  modules_install

# update rootfs modules
rm -rf rootfs/var/cache/apk/*
rm -rf rootfs/lib/modules/*
rm -f rootfs/root/.ash_history
rm -f rootfs/var/log/apk.log
rm -rf rootfs/lib/modules/*

du -sh rootfs

# rebuild kernel archive rootfs
make ARCH=arm64 \
  CROSS_COMPILE=aarch64-linux-gnu- \
  KBUILD_BUILD_USER="builder" \
  KBUILD_BUILD_HOST="kdevbuilder" \
  LOCALVERSION=-kdev \
  KCFLAGS="-Wno-unused-function" \
  -j$(nproc)

cat include/config/kernel.release
ls -alh arch/arm64/boot/Image

rm -rf ${WORKDIR}/release
rm -rf recovery
mkdir -p recovery

# archive kernel image
xz --format=lzma -k -c arch/arm64/boot/Image >recovery/kernel.lzma
ls -al recovery/kernel.lzma

# generate recovery
cd ${WORKDIR}/recovery
dd if=/dev/zero of=recovery.img bs=1M count=63

# Compress kernel with LZMA
xz --format=lzma -k -c ${WORKDIR}/arch/arm64/boot/Image >kernel.lzma

# Use IH_TYPE_KERNEL_NOLOAD so U-Boot allocates a separate buffer
# for decompression, avoiding in-place overwrite (85MB uncompressed
# would corrupt the 47MB compressed stream at the same address)
mkimage -A arm64 -O linux -T kernel_noload -C lzma \
  -a 0x40080000 -e 0x40080000 \
  -n "Recovery Kernel" \
  -d kernel.lzma kernel-uImage.lzma
mkimage -l kernel-uImage.lzma

mkfs.ext4 \
  -L recovery \
  recovery.img

cleanup() { mountpoint -q /mnt && umount /mnt || true; }
trap cleanup EXIT

mount recovery.img /mnt
rmdir /mnt/lost+found || :

cp -a ${WORKDIR}/arch/arm64/boot/dts/rockchip/rk3588-aigo-s60.dtb \
  /mnt/rk3588-aigo-s60.dtb
cp -a kernel-uImage.lzma /mnt
tee /mnt/recovery.conf <<'EOF'
label RK3588 Linux recovery
    kernel kernel-uImage.lzma
    fdt rk3588-aigo-s60.dtb
    append console=ttyS2,1500000n8 earlycon=uart8250,mmio32,0xfeb50000 keep rw fw_devlink=off deferred_probe_timeout=1 initcall_debug no_console_suspend panic=1 loglevel=8 irqchip.gicv3_pseudo_nmi=0 irqchip.gicv3_nolpi=1
EOF
umount /mnt
sync
mkdir -p ${WORKDIR}/release/
cp -a recovery.img ${WORKDIR}/release/recovery.img
cp -a recovery.img ${WORKDIR}/release/recovery_${TIMESTAMP}.img

cp -a kernel-uImage.lzma ${WORKDIR}/release/kernel-uImage.lzma
cp -a kernel-uImage.lzma ${WORKDIR}/release/kernel-uImage_${TIMESTAMP}.lzma

# Generate recovery.conf for /hack/ directory (U-Boot reads from eMMC)
# Paths are relative to partition root, so /hack/ prefix is needed
tee ${WORKDIR}/release/recovery-hack.conf <<'EOF'
label RK3588 Linux recovery
    kernel /hack/kernel-uImage.lzma
    fdt /hack/rk3588-aigo-s60.dtb
    append console=ttyS2,1500000n8 earlycon=uart8250,mmio32,0xfeb50000 keep rw fw_devlink=off deferred_probe_timeout=1 initcall_debug no_console_suspend panic=1 loglevel=8 irqchip.gicv3_pseudo_nmi=0 irqchip.gicv3_nolpi=1
EOF

cp -a ${WORKDIR}/arch/arm64/boot/dts/rockchip/rk3588-aigo-s60.dtb \
  ${WORKDIR}/release/rk3588-aigo-s60.dtb

sync

ls -alh ${WORKDIR}/release/*.img
sha256sum ${WORKDIR}/release/*.img
ls -alh ${WORKDIR}/recovery/kernel.lzma
sha256sum ${WORKDIR}/recovery/kernel.lzma

echo "All done!All ok!"
