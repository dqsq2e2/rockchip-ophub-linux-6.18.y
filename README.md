# 9Tripod I3588 恢复系统

本分支适配九鼎 I3588（RK3588）的 Linux 6.18 恢复内核、设备树与网页刷写助手。它与 PHX-R3588、AIUI、G98 是不同板型，不能混用设备树或固件。

## 构建与安装

内核配置为 `i3588_recovery_defconfig`，设备树为 `rockchip/rk3588-9tripod-I3588.dtb`。完整镜像通过 `scripts/build-i3588.sh` 构建；依赖、交叉构建条件、板载固件路径和系统分区映射说明见 [板级文档](board/9tripod-i3588/README.md)。GitHub Actions 构建并测试恢复程序、内核、模块及设备树。

恢复镜像与系统镜像是两个独立的 raw GPT 整盘 IMG，均包含对应的主线 U-Boot。恢复镜像启动后可通过网页安装系统：完整系统放在 LBA 524288，其分区映射到外层 GPT，保留恢复分区；也可把系统 IMG 直接从 LBA 0 写入整盘。它们不是瑞芯微 RKFW `update.img`。配套 [U-Boot 分支](https://github.com/dqsq2e2/rockchip-uboot-mainline/tree/9tripod-i3588)。

## 按键与验证

恢复系统中，PCIe 网口作为 WAN 自动获取地址，板载 GMAC 网口作为 LAN；LAN 与 Wi-Fi AP 桥接在 `192.168.88.1/24`。Wi-Fi 名称 `I3588-Recovery`，密码 `I3588recovery`；LAN DHCP 地址范围为 `192.168.88.100–192.168.88.200`。HDMI 显示访问地址，并在 WAN 地址变化时刷新。

SW1 VOL+/RECOVERY 配置为检测到 USB 主机握手时请求 Maskrom，否则进入已安装的恢复系统；SW7 BOOT 硬件直接进入 Maskrom；SW5 为电源、SW6 为复位、SW2 为音量减。物理按键与线缆组合尚未实测。恢复串口为 1500000 baud，恢复 SSH/串口默认 root/root。

只对 eMMC 做过存储启动测试，NVMe、SATA、USB、TF 的实际启动尚未验证。HDMI RX 的 160 MiB 池可复用为 CMA；采集仍需 HDMI 输入信号验证。
