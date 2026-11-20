#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-2.0+
set -euo pipefail
source_dir=${1:?Usage: build-aiot-3588a2-kernel.sh KERNEL_SOURCE OUTPUT}
output=${2:?Usage: build-aiot-3588a2-kernel.sh KERNEL_SOURCE OUTPUT}
output=$(realpath -m "$output")
export ARCH=arm64 CROSS_COMPILE=${CROSS_COMPILE:-aarch64-linux-gnu-}
export KBUILD_BUILD_USER=builder KBUILD_BUILD_HOST=aiot-3588a2-recovery
make -C "$source_dir" O="$output" i3588_recovery_defconfig
"$source_dir/scripts/config" --file "$output/.config" \
    --set-str INITRAMFS_SOURCE '' --set-str LOCALVERSION '-kdev' \
    --enable GPIO_PCA953X --enable SENSORS_PWM_FAN \
    --enable RTC_DRV_HYM8563 --enable MMC_PWRSEQ_SIMPLE \
    --enable KEYBOARD_ADC --enable BRCMFMAC_SDIO \
    --enable TYPEC --enable TYPEC_TCPM --enable TYPEC_RT1711H \
    --enable SND_SIMPLE_CARD --module SND_SOC_ES8328 --module SND_SOC_ES8328_I2C
make -C "$source_dir" O="$output" olddefconfig
make -C "$source_dir" O="$output" \
    rockchip/rk3588-aiot-3588a2.dtb rockchip/rk3588-aiot-3588a2-fnos.dtb
make -C "$source_dir" O="$output" -j"${JOBS:-8}" Image modules
