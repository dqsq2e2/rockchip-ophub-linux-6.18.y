# LUNZN RK3588 AIUI 恢复系统

本分支包含 AIUI 板的 Linux 6.18 设备树、恢复内核配置和网页刷写助手，基于 ophub 内核及既有恢复工程适配。

## 源码与构建

- 设备树：`arch/arm64/boot/dts/rockchip/rk3588-AIUI.dts`
- 内核配置：`arch/arm64/configs/rk3588_aiui_recovery_defconfig`
- 内核与设备树构建入口：`kdev-build.sh`、`kdev-dtbs.sh`
- 网页刷写助手及测试：`rkdev/`，可在该目录运行 `go test ./...`
- 配套 [AIUI U-Boot 分支](https://github.com/dqsq2e2/rockchip-uboot-mainline/tree/rk3588-aiui)

`kdev-recovery.sh` 和继承的整盘打包工作流尚未适配 AIUI。上述构建入口用于内核、设备树和恢复程序，不生成完整的 AIUI 整盘镜像。
