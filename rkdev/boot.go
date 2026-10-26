package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Keep these in sync with include/configs/lunzn_aiui.h. All filesystem
// partitions are scanned; the eMMC recovery area is reserved explicitly.
const lunznScan = "setenv lunzn_parts; if part list ${devtype} ${devnum} lunzn_parts; then " +
	"for distro_bootpart in ${lunzn_parts}; do " +
	"if test ${devtype} = mmc && test ${devnum} = 0; then " +
	"if part start mmc 0 0x${distro_bootpart} lunzn_start; then " +
	"if itest.l ${lunzn_start} -ge 0x80000; then run lunzn_boot_part; fi; " +
	"fi; else run lunzn_boot_part; fi; done; fi"

const lunznBootPart = "for prefix in / /boot/; do " +
	"if test -e ${devtype} ${devnum}:${distro_bootpart} ${prefix}extlinux/extlinux.conf; then " +
	"sysboot ${devtype} ${devnum}:${distro_bootpart} any ${scriptaddr} ${prefix}extlinux/extlinux.conf; fi; " +
	"if test -e ${devtype} ${devnum}:${distro_bootpart} ${prefix}boot.scr; then " +
	"if load ${devtype} ${devnum}:${distro_bootpart} ${scriptaddr} ${prefix}boot.scr; then source ${scriptaddr}; fi; fi; done"

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
	for _, key := range []string{"boot_system_usb", "boot_system_nvme", "boot_system_sd", "boot_system_emmc", "lunzn_scan", "lunzn_boot_part"} {
		if values[key] != ubootDefaultVars[key] {
			fmt.Fprintf(&updates, "%s %s\n", key, ubootDefaultVars[key])
		}
	}
	if updates.Len() == 0 {
		return nil
	}
	file, err := os.CreateTemp("/tmp", "lunzn-boot-env-*")
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
