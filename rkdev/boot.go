package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// The factory disk places its boot partition over the PHX environment offsets.
// RAM recovery may inspect that disk, but must never access its environment.
func validateEnvironmentLayout(parts []gptPartition) error {
	if _, err := recoveryPartition(parts); err != nil {
		return fmt.Errorf("不是配套 PHX 恢复布局，拒绝访问 U-Boot 环境变量: %w", err)
	}
	return nil
}

// Keep these in sync with include/configs/phx_r3588.h. All filesystem
// partitions are scanned; the eMMC recovery area is reserved explicitly.
const phxScan = "setenv phx_parts; if part list ${devtype} ${devnum} phx_parts; then " +
	"for distro_bootpart in ${phx_parts}; do " +
	"if part name ${devtype} ${devnum} 0x${distro_bootpart} phx_name; then " +
	"if test \"${phx_name}\" != PHX_RECOVERY; then run phx_boot_part; fi; " +
	"fi; done; fi"

const phxBootPart = "for prefix in / /boot/; do " +
	"if test -e ${devtype} ${devnum}:${distro_bootpart} ${prefix}extlinux/extlinux.conf; then " +
	"sysboot ${devtype} ${devnum}:${distro_bootpart} any ${scriptaddr} ${prefix}extlinux/extlinux.conf; fi; " +
	"if test -e ${devtype} ${devnum}:${distro_bootpart} ${prefix}boot.scr; then " +
	"if load ${devtype} ${devnum}:${distro_bootpart} ${scriptaddr} ${prefix}boot.scr; then source ${scriptaddr}; fi; fi; done"

const phxRecoveryPart = "if part name ${devtype} ${devnum} 1 phx_name; then " +
	"if test \"${phx_name}\" = PHX_RECOVERY; then " +
	"sysboot ${devtype} ${devnum}:1 any ${scriptaddr} /extlinux/extlinux.conf; fi; fi"

const phxRecoveryBoot = "echo PHX-R3588 Recovery; setenv devtype mmc; " +
	"for devnum in 0 1; do if mmc dev ${devnum} && mmc rescan; then run phx_recovery_part; fi; done; " +
	"usb start; setenv devtype usb; for devnum in 0 1; do if usb dev ${devnum}; then run phx_recovery_part; fi; done; " +
	"pci enum; nvme scan; setenv devtype nvme; for devnum in 0 1; do if nvme dev ${devnum}; then run phx_recovery_part; fi; done"

func installBootScanner() error {
	if err := ensureFwEnvConfig(); err != nil {
		return err
	}
	out, err := exec.Command("fw_printenv").Output()
	if err != nil {
		return fmt.Errorf("读取引导环境失败: %w", err)
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = value
		}
	}
	var updates strings.Builder
	for _, key := range []string{"boot_system_usb", "boot_system_nvme", "boot_system_sd", "boot_system_emmc", "phx_scan", "phx_boot_part", "phx_recovery_part", "recovery_boot"} {
		if values[key] != ubootDefaultVars[key] {
			fmt.Fprintf(&updates, "%s %s\n", key, ubootDefaultVars[key])
		}
	}
	if updates.Len() == 0 {
		return nil
	}
	file, err := os.CreateTemp("/tmp", "phx-boot-env-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.WriteString(updates.String()); err != nil {
		file.Close()
		return err
	}
	file.Close()
	if out, err := exec.Command("fw_setenv", "-s", file.Name()).CombinedOutput(); err != nil {
		return fmt.Errorf("更新分区扫描命令失败: %v: %s", err, out)
	}
	return nil
}
