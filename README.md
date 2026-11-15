# LUNZN RK3588 AIUI 恢复系统

本分支包含 AIUI 板的 Linux 6.18 设备树、恢复内核配置和网页刷写助手，基于 ophub 内核及既有恢复工程适配。AIUI 没有 G98 的 SPI NOR 配置，不能使用 G98 的 SPI 镜像或分区说明。

## 源码与构建

- 设备树：`arch/arm64/boot/dts/rockchip/rk3588-AIUI.dts`
- 内核配置：`arch/arm64/configs/rk3588_aiui_recovery_defconfig`
- 内核与设备树构建入口：`kdev-build.sh`、`kdev-dtbs.sh`
- 网页刷写助手及测试：`rkdev/`，可在该目录运行 `go test ./...`
- 配套 [AIUI U-Boot 分支](https://github.com/dqsq2e2/rockchip-uboot-mainline/tree/rk3588-aiui)

`kdev-recovery.sh` 和部分上游工作流仍包含 G98 专用的 SPI/eMMC 打包逻辑，不能直接作为 AIUI 成品镜像的构建或刷写说明。请结合本板实际引导布局和已验证的设备专用构建流程制作镜像。

适配代码、构建成功、镜像验证和真实设备测试是不同阶段；外设和启动介质应以相应实测结果为准。
