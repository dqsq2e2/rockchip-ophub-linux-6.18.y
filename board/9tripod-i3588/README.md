# 9Tripod I3588 recovery

Build the kernel with `i3588_recovery_defconfig`. The board DTB is
`rockchip/rk3588-9tripod-I3588.dtb`. The tested CV1 core uses software I2C on
GPIO0_D1/GPIO0_D2 for its two CPU regulators; hardware I2C0 remains disabled.

For a complete raw image run `scripts/build-i3588.sh` as root in an AArch64
environment, or with qemu-user-static/binfmt configured. Set
`I3588_UBOOT_DIR` to the matching mainline U-Boot artifacts and
`I3588_FIRMWARE_DIR` to the board firmware directory. The latter contains
`brcm/brcmfmac43752-pcie.{bin,txt,clm_blob}`, `brcm/BCM4362A2.hcd` and
`arm/mali/arch10.8/mali_csffw.bin`. Use the board's AP6275P calibration rather
than calibration from another product. Native dependencies include Go,
the AArch64 compiler, kernel build tools, kmod, cpio, pigz, e2fsprogs, gdisk,
u-boot-tools and an Alpine package mirror reachable by the rootfs.

The independent recovery image includes U-Boot and redundant default
environments. Recovery occupies sectors 65536–524253. The complete system
image starts at sector 524288; its GPT partitions are projected into the
outer GPT without changing their PARTUUIDs. The upload handler preserves
the active loader, environments and recovery partition when installing a
system through recovery. A system image can also be written directly as a
whole disk when recovery is not needed.

The web assistant's build date is injected by `build-i3588-app.sh`; invoking
plain `go build` leaves the fallback version `unknown`. The rootfs creates
SSH host keys on first boot and offers root/root for serial and SSH access.

The PCIe Ethernet controller is the DHCP WAN; the onboard GMAC is the LAN.
The LAN and Wi-Fi AP share bridge br0 at 192.168.88.1/24. The AP SSID is
`I3588-Recovery` and its WPA2 password is `I3588recovery`. DHCP leases span
192.168.88.100–192.168.88.200 and WAN traffic is masqueraded. HDMI displays
the local recovery URLs and refreshes them when the WAN address changes.

SW1 (VOL+/RECOVERY) selects recovery without a USB host, or requests Maskrom
after a USB host handshake. SW7 BOOT directly forces Maskrom in BootROM and
is not the software recovery selector. SW5 is PMIC power, SW6 hardware reset,
and SW2 volume-down. eMMC boot has been tested; NVMe, SATA, USB and TF boot
remain untested. Physical key behavior requires actual key presses.
