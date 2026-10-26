#!/bin/bash

set -ex

WORKDIR=$(pwd)

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
  rk3588_aiui_recovery_defconfig

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
  dtbs \
  -j$(nproc)

dtc -I dtb -O dts arch/arm64/boot/dts/rockchip/rk3588-AIUI.dtb -o /tmp/rk3588-AIUI.dts

echo "All done! All ok!"
