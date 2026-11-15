# I3588 system image adaptation

This directory contains board wiring conversion, optional DKMS modules, and
image assembly scripts. Obtain the unmodified
`fnos_Mainland-PE_arm_1.2.0701_nanopc-t6_2882.img` separately. No operating
system image, factory backup, radio calibration, credentials, or build
outputs are included in this source tree.

Use Linux as root with AArch64 execution available (native or qemu-user
binfmt), Python 3, dtc, gdisk, u-boot-tools, Btrfs/ext4 tools, DKMS and normal
kernel build dependencies. Supply these inputs:

- The donor image and its original `rk3588-nanopc-t6.dtb`.
- The tested board's original runtime DTB and radio firmware/calibration.
- Factory and target kernel clock headers; retain the target headers'
  `dt-bindings/soc/rockchip,vop2.h` alongside `dt-bindings/clock/`.
- Matching U-Boot `u-boot-rockchip.bin` and `initial-env.txt` artifacts.
- A firmware directory containing `brcm/brcmfmac43752-pcie.bin`, `.txt`,
  `.clm_blob`, `brcm/BCM4362A2.hcd` and
  `arm/mali/arch10.8/mali_csffw.bin`.

Generate the board DTB with explicit inputs:

```sh
python3 build_board.py \
  --base /assets/rk3588-nanopc-t6.dtb \
  --factory /assets/i3588-factory.dtb \
  --legacy-clock-header /assets/factory/rk3588-cru.h \
  --clock-header /assets/target/dt-bindings/clock/rockchip,rk3588-cru.h \
  --output-dir /build/i3588-dtb \
  --legacy-mclkout --complete-display-graph --isolate-vp2-pll
```

Build with a separate work directory and output path:

```sh
export I3588_BASE_IMAGE=/assets/fnos_Mainland-PE_arm_1.2.0701_nanopc-t6_2882.img
export I3588_UBOOT_DIR=/assets/uboot
export I3588_FIRMWARE_DIR=/assets/firmware
export I3588_OS_DTB=/build/i3588-dtb/rk3588-9tripod-I3588.dtb
export I3588_WORK_DIR=/build/i3588-system
export I3588_BOOT_MOUNT=/build/i3588-boot
export I3588_ROOT_MOUNT=/build/i3588-root
export I3588_OUTPUT_DIR=/output
export I3588_OUTPUT_IMAGE=/output/fnos_Mainland-PE_arm_1.2.0701_9tripod-I3588_2882.img
bash build-fnos-image.sh
```

The script uses the pinned donor's partition offsets and
`6.18.18.c1145-trim` kernel. It refuses to replace an existing build unless
`--resume` is supplied. Use clean, separate mount/work paths and keep an
untouched donor image. Package downloads need access to the donor's package
repositories. Installed DKMS sources cover GPIO fan, CAN-FD, Panthor,
ST accelerometer and HDMI RX. The supplied GPU device tree currently selects
the distribution's Mali driver; installing Panthor does not mean it binds.

Kernel package hooks only queue work. The worker waits for dpkg locks and
complete matching headers, builds the required missing drivers, verifies
their installed status, and generates the initramfs before atomically
switching the selected boot kernel. Failures preserve the previous boot
selection. A module supplied natively by a newer kernel supersedes its DKMS
replacement. Run `python3 test_kernel_update.py` as root on Linux for seven
integration cases, including delayed headers and failed builds/initramfs.

The resulting raw disk image contains its own U-Boot and can be written to
an entire target disk with `dd`, or installed through the separate recovery
image's upload page. Recovery retains its loader and partition, stores the
system at sector 524288, and maps the inner partitions into the outer GPT.
Raw images here are not Rockchip RKFW/update.img containers; choose raw
whole-disk/LBA writing in compatible flashing tools.

Both eMMC whole-disk writing and the recovery application's real upload
installation have been tested. See the board validation report for the
tested image hashes and cold-boot checks. eMMC boot has been tested; external
NVMe, SATA, USB and TF boot remain untested.

The codec's I2S0 TX/RX source clocks use GPLL. Display initialization changes
AUPLL after the initial codec clock setup, otherwise invalidating the codec
MCLK before sound-card registration. Cold boot and 44.1/48 kHz ALSA playback
and capture DMA passed; physical jack loopback has not been tested.

The distribution network service owns Bluetooth's HCI USER_CHANNEL while
advertising. Stopping it temporarily allowed an actual BLE scan (seven device
events in eight seconds); the service was restored afterward. Pairing and
data transfer remain untested. The vendor IEP module prints its ROI failure
message on a non-NULL allocation; cold-boot I5O2 hardware deinterlacing passed
ten iterations with twenty verified output frames without reloading it.

The follow-up recovery CI 43670263615 passed the application tests and board
kernel/module build after the codec generator update.

Optional external display/camera panels require their own panel-specific
wiring and timing configuration. Hardware workload results do not establish
electrical validation of unconnected peripherals.
