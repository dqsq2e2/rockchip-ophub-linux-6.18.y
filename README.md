# 视美泰 AIOT-3588A V2.0 恢复系统

本分支提供 AIOT-3588A V2.0 的恢复内核、`rk3588-aiot-3588a2.dtb` 和网页刷写助手。配套主线 U-Boot 位于 [aiot-3588a2 分支](https://github.com/dqsq2e2/rockchip-uboot-mainline/tree/aiot-3588a2)。

`aiot-3588a2-recovery.img` 是包含主线 U-Boot 的 raw GPT 整盘镜像，从 LBA 0 写入。恢复助手可将独立系统 IMG 写入 `AIOT_SYSTEM` 区域，并将内部 GPT 分区映射到外层分区表，保留恢复分区。也可以直接从 LBA 0 写入包含 U-Boot 的系统 IMG。

上电时按住板上唯一的下载键。有 USB 主机握手时请求进入 Maskrom，否则尝试启动已安装的恢复分区。TTL1 串口使用 1500000 baud、8N1。

恢复系统的板载网口使用 DHCP。连接开放的 Wi-Fi AP `AIOT-3588A2-Recovery` 后，在 `http://192.168.88.1/` 打开恢复网页。HDMI 显示启动 Logo 和登录信息，有线地址变化时在原位置更新。内核日志通过 TTL1 输出。SSH 和串口默认账号均为 `root`，密码为 `root`。

构建时运行 `scripts/build-aiot-3588a2.sh`，通过 `AIOT3588A2_UBOOT_DIR` 指定配套的 `u-boot-rockchip.bin` 和 `u-boot-initial-env`，通过 `AIOT3588A2_FIRMWARE_DIR` 指定 AP6256 对应的固件和板级 NVRAM。脚本输出整盘镜像、恢复分区镜像和 SHA256 校验文件。
