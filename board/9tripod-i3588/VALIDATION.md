# I3588 validation record — 2026-11-15

Test unit: 4 GB LPDDR4X, CV1 CPU regulator wiring, BV2 carrier references,
15,634,268,160-byte eMMC. Console: UART2, 1500000 baud. Results apply to this
unit and the selected boot firmware; they do not validate other core revisions.

The original necessary system partitions/files were backed up and checked
before testing. User data and unused space were excluded.

| Check | Evidence/result |
| --- | --- |
| Mainline boot chain | Factory DDR v1.08, pinned upstream TF-A; eMMC recovery and system cold boot |
| Memory | Native factory memory map, no `mem=`; locked 2800 MiB memtester full iteration passed |
| Recovery image | 512 MiB full eMMC readback matched `45fee706dd65125cea934e5841b30acbbddf192705ad70626ed3e0ed02c811aa`; cold boot passed |
| Direct system installation | Full image readback `1954c4bd327e1becd4dccae2a89a0109b658337f7febdaa129d6d9eb4b4e22f9`, cold boot, Btrfs expansion, web HTTP200 |
| Latest system image | `0a76de26c7e31a2f7d8c54dc1d298359f8acc74f6ee2ef8c29db90637d0dc10c`; adds the live-tested foreground nmbd override |
| Recovery upload | Latest image installed through real HTTP `/upload`; source bytes read back and verified; loader, environments and recovery hash unchanged |
| Installed payload | 76,431 root files identical; 527 BOOT files with only the two expected mapping-aware boot script changes |
| Latest mapped cold boot | eMMC p3 root expanded to 13.7 GiB, GPT verified, five DKMS packages installed, maintenance timer active with no pending work, web HTTP200, nmbd/smbd active |
| Partition mapping | Inner BOOT/root GPT entries projected at sector524288 while retaining their PARTUUIDs |
| Upgrade sequencing | Seven disposable-chroot/real-lock integration tests passed, including missing headers, failed DKMS, missing installed modules, failed initramfs and native replacement |
| DTB reproduction | Explicit-input conversion SHA256 `e07d7c8db78dc02070f2eb045a81f3c6b2d6c43847ef35368722992316405aed`, matches installed DTB |
| CPU and network | All eight CPUs, three frequency policies; Ethernet connectivity and system web UI |
| Media engines | H264 encode/decode 60 frames each; encoder OPP binding corrected; devfreq scaling not established |
| GPU/NPU | Distribution Mali driver bound; NPU driver/device enumerated; no inference or GPU workload acceptance |
| Audio/input | ES8388 and both HDMI ALSA cards enumerated; SC7A20 register read; no full audio loopback |
| WLAN | Actual 2.4/5 GHz scan; AP association not tested |

Limits still requiring work or physical fixtures:

- Bluetooth firmware loads and hci0 exists, but management controller lists
  are empty. Bluetooth operation has not passed acceptance.
- IEP ROI allocation logs an error despite a later allocation on the same
  device succeeding. Full deinterlacing is not accepted as working.
- NVMe, SATA, USB and TF boot code/initramfs are configured. The user limited
  physical storage testing to eMMC; external-media boot remains untested.
- With only eMMC and no UPS configured, the donor SMART daemon and UPS
  server report missing monitored devices. These services were not hidden
  or disabled to make the failed-unit list empty.
- SW1 recovery/host handshake behavior needs physical key and cable tests
  on this board. SW7 BOOT is unconditional hardware Maskrom and cannot be
  changed to conditional software behavior.
- External display panels/cameras, CAN transceivers, fan electrical behavior,
  audio jacks and peripheral cables need their matching physical equipment.
- Later DDR binaries expose an extra high bank with repeatable failures near
  its end on this test unit. This is neither accepted nor diagnosed as a
  hardware lock or proven hardware damage.

These checks are an explicit validation boundary, not a declaration that
every peripheral or the entire adaptation goal is complete.
