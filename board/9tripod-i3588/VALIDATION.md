# I3588 validation record — 2026-11-15

Tested hardware: 4 GB LPDDR4X CV1 core, BV2 carrier references,
15,634,268,160-byte eMMC, CID150100414a5444345206a5434cc4b800.
UART2 at 1500000 baud. Storage boot tests cover eMMC.
Original necessary partitions/files were backed up and checked, excluding user data/free space.

| Check | Result |
| --- | --- |
| Final system raw IMG | `7318543b3519d65a95666b140c20de650e91ee383ec29623ef7a2c1cbb78b707`, 3859808256 bytes |
| Recovery raw IMG | `45fee706dd65125cea934e5841b30acbbddf192705ad70626ed3e0ed02c811aa`, 536870912 bytes |
| Embedded mainline loader | `879497d989f79b3151444da07a693ade60f94eac2793ed2246a0f0a9b2c96662` |
| System DTB reproduction | `a9462eac8b3eb48daf4d4015441677f5a1a54dc73a57a1b3fac9bb988e9fde48`; independent explicit-input source regeneration matches both installed copies |
| Boot firmware | Factory DDR v1.08 plus pinned upstream TF-A; native memory map, no mem= override |
| Memory | Locked 2800 MiB memtester full iteration passed |
| Recovery | Full 512 MiB eMMC readback matched; actual P2 cold boot and web assistant v2026.11.14 |
| Real recovery upload | Final IMG through HTTP /upload; complete source-byte readback matched; loader/environment/recovery unchanged |
| Installed payload | 76431 root files identical; 527 BOOT files, only boot.cmd/boot.scr mapping changes |
| Mapped cold boot | eMMC root p3 expanded to13.7GiB; GPT valid, webHTTP200, smbd/nmbd active, five DKMS installed, upgrade queue empty |
| Whole-disk direct write | Same final IMG fully read back before test-only rescue payload; matched SHA; P2 cold boot passed |
| Direct cold boot | eMMC root p2 expanded to14GiB; same GPT/web/SMB/DKMS checks passed |
| Clean distribution payload | Empty machine-id/logs; no SSH keys, leases/connection profiles, package cache or recovery-test payload; embedded loader identical |
| Upgrade sequencing | Seven disposable-chroot/real-lock integration cases: absent headers, failed DKMS, missing installed modules, failed initramfs, native replacement, ordered commit, POSIX dpkg locking |
| CPU/network | Eight CPUs, policies0/4/6, Ethernet and web UI |
| Audio | Stable GPLL I2S0 parents prevent AUPLL display retuning from invalidating codec MCLK; three ALSA cards after both final-image cold boots; ES8388 44100/48000Hz playback/capture DMA passed on both layouts |
| WLAN | Actual2.4/5GHz scan; association not tested |
| Bluetooth | Actual8-second BLE discovery, seven device events after temporary release of network service HCI USER_CHANNEL; service restored |
| H264 | Hardware encode/decode60frames each; corrected encoder OPP, DVFS not established |
| IEP | Original module on cold boot, no reload: ten I5O2 hardware iterations, twenty720x480NV12 frames/10368000bytes, nonempty and changing output |
| GPU/NPU/input | Distribution Mali bound, optional Panthor DKMS available; NPU3cores, HDMI RX video node and SC7A20 register read |

IEP's ROI warning is inverted in the vendor module: the error string is reached
for a non-NULL allocation. Hardware processing above establishes functional
deinterlacing for this case without a production driver change.
Bluetooth's empty management lists during normal advertising reflect the
network service's HCI USER_CHANNEL ownership; they do not establish a driver failure.

Factory DDR1.08 is retained. Later DDR's extra high bank had repeatable
errors on this unit; the released images retain DDR v1.08.
Pins: rkbin35c3e2e438d2a6983a958fd2d3a66076112bea23,
TF-A a881364e03b0c5b3a8c00f20ad6fbdda53a57031.

Upgrade hooks enqueue; complete matching headers, successful build/installed
checks and initramfs precede atomic boot selection. No synchronous DKMS
requirement is imposed while matching headers are still unavailable.

## Source and builds

U-Boot branch9tripod-i3588: c7f41c48162bab0e520500a300e0f71c44883d77, CI43667005404 passed.
Recovery branch9tripod-i3588-recovery: functional source e75eb6819e5567e8729773b06e4fa1f8e545ecae.
Production recovery kernel/app originate from fdbc61aa6c7dbdeb1bbe391de0415f0a85028370,
CI43667577784 passed. Follow-up CI43670263615 passed for the codec generator
and recovery kernel/module build.
Board system-adaptation/ contains source only, without original backups,
firmware binaries, images, private keys or credentials.

## Physical limits

- NVMe/SATA/USB/TF boot configured, not physically tested.
- SW7 BOOT grounds ADC0: unconditional BootROM Maskrom. SW1 VOL+/RECOVERY
  implements USB-host-handshake Maskrom/otherwise installed recovery.
  Physical button/cable combinations remain untested.
- SW5 PMIC power, SW6 hardware reset, SW2 volume-down are configured by wiring.
- No Bluetooth pairing/transfer, Wi-Fi association, audio jack loopback,
  HDMI RX input, GPU stress or NPU inference acceptance.
- Display panels/cameras, CAN transceivers and fan electrical behavior need
  their corresponding physical equipment. Encoder DVFS remains unestablished.
- SMART/UPS require supported monitored devices; eMMC-only configurations without a UPS may report missing devices.

Local evidence: _work/i3588-audio-recovery-flash.log,
i3588-audio-recovery-coldboot.log, i3588-audio-mapped-install.log,
i3588-audio-mapped-payload-verification.json, i3588-audio-mapped-coldboot.log,
i3588-audio-mapped-acceptance.json, i3588-audio-mapped-pcm.log,
i3588-audio-direct-flash.log, i3588-audio-direct-coldboot.log,
i3588-audio-direct-acceptance.json, i3588-audio-direct-pcm.log,
i3588-release-clean-audit.json, i3588-bt-radio-test.log,
i3588-iep-cold-hardware-test.log and i3588-kernel-update-tests.log.
