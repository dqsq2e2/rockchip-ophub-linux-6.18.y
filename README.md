# PHX-R3588 恢复系统

本分支适配 PHX-R3588 V02 底板的 Linux 6.18 恢复内核、设备树和网页刷写助手。请勿套用 G98、AIUI 或 I3588 的硬件配置。

## 构建与镜像

运行 `scripts/build-phx-r3588.sh` 构建独立的恢复系统整盘镜像；工具链、固件目录和详细分区布局见 [板级文档](board/phx-r3588/README.md)。设备树为 `rk3588-phx-r3588.dtb`；适配需使用本板 AP6275P 校准文件。配套 [U-Boot 分支](https://github.com/dqsq2e2/rockchip-uboot-mainline/tree/phx-r3588)。

恢复系统 IMG 与系统 IMG 各自包含主线 U-Boot，均为从 LBA 0 写入的 raw GPT 整盘镜像，并非 RKFW `update.img`。系统可直接整盘写入，也可在恢复网页中上传：系统镜像放在 LBA 524288，内部系统分区映射到外层 GPT，并保留恢复区和引导环境。

串口为 115200 baud，恢复系统默认 root/root。下载键已实测：连接电脑时请求 Maskrom，未连接电脑时进入已安装恢复系统；整盘直刷系统后没有常驻恢复分区。

## 验证范围

已进行 eMMC 完整写入、读回和启动测试；网口及 Wi-Fi 双频扫描已验证。NVMe、USB、TF 的外接介质启动、显示、摄像头、CAN 电气和音频仍需相应硬件实测。驱动枚举不等同于完整功能验收。
