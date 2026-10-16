#!/bin/bash
#
# RK3588 AIoT 3588IED — 目标环境外设检测脚本
# 用法: bash check-env.sh [目标IP]
#

TARGET_IP="${1:-192.168.33.39}"
TARGET_USER="root"
TARGET_PASS="admin"

SSH="sshpass -p ${TARGET_PASS} ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10 ${TARGET_USER}@${TARGET_IP}"

# 颜色
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

PASS=0
WARN=0
FAIL=0

ok()   { echo -e "  ${GREEN}✓${NC} $1"; ((PASS++)); }
warn() { echo -e "  ${YELLOW}⚠${NC} $1"; ((WARN++)); }
fail() { echo -e "  ${RED}✗${NC} $1"; ((FAIL++)); }
hdr()  { echo -e "\n${BOLD}${CYAN}[$1]${NC}"; }

echo -e "${BOLD}${CYAN}"
echo "============================================"
echo " RK3588 AIoT 3588IED — 外设检测脚本"
echo " Target: ${TARGET_IP}"
echo " Date:   $(date '+%Y-%m-%d %H:%M:%S')"
echo "============================================"
echo -e "${NC}"

# ── 系统基本信息 ──────────────────────────────
hdr "系统基本信息"

UNAME=$($SSH 'uname -r' 2>/dev/null)
if [ -n "$UNAME" ]; then
    ok "内核: ${UNAME}"
    ARCH=$($SSH 'uname -m' 2>/dev/null)
    ok "架构: ${ARCH}"
else
    fail "SSH 连接失败 — ${TARGET_IP}"
    exit 1
fi

MODEL=$($SSH 'cat /sys/firmware/devicetree/base/model 2>/dev/null || cat /proc/device-tree/model 2>/dev/null' 2>/dev/null | tr -d '\0')
[ -n "$MODEL" ] && ok "设备型号: ${MODEL}" || warn "设备型号未找到"

CPUINFO=$($SSH 'cat /proc/cpuinfo | grep -c "^processor"' 2>/dev/null)
ok "CPU 核心数: ${CPUINFO}"

MEMTOTAL=$($SSH 'awk "/MemTotal/{print \$2}" /proc/meminfo' 2>/dev/null)
ok "内存: $((MEMTOTAL/1024)) MB"

UPTIME=$($SSH 'uptime -p' 2>/dev/null)
ok "运行时间: ${UPTIME}"

# ── WiFi ──────────────────────────────────────
hdr "WiFi (BCM43752 / brcmfmac)"

WLAN_STATE=$($SSH 'nmcli -t -f DEVICE,STATE device status 2>/dev/null | grep "^wlan0:" | cut -d: -f2' 2>/dev/null)
if [ "$WLAN_STATE" = "connected" ] || [ "$WLAN_STATE" = "disconnected" ]; then
    ok "wlan0 接口存在 (${WLAN_STATE})"
else
    fail "wlan0 接口不存在"
fi

WLAN_SCAN=$($SSH 'nmcli -t -f SSID device wifi list 2>/dev/null | grep -v "^--" | head -1' 2>/dev/null)
if [ -n "$WLAN_SCAN" ]; then
    ok "WiFi 扫描正常 (发现 SSID: ${WLAN_SCAN})"
else
    warn "WiFi 扫描无结果 (可能未连接或信号弱)"
fi

BRCMFMAC=$($SSH 'lsmod 2>/dev/null | grep "^brcmfmac "' 2>/dev/null)
if [ -n "$BRCMFMAC" ]; then
    ok "brcmfmac 驱动已加载"
else
    fail "brcmfmac 驱动未加载"
fi

WLAN_MAC=$($SSH 'cat /sys/class/net/wlan0/address 2>/dev/null' 2>/dev/null)
[ -n "$WLAN_MAC" ] && ok "WiFi MAC: ${WLAN_MAC}" || warn "WiFi MAC 未获取"

# ── Bluetooth ─────────────────────────────────
hdr "Bluetooth (BCM43752 BT / hci_bcm)"

BT_STATE=$($SSH 'hciconfig hci0 2>/dev/null | grep -o "UP RUNNING"' 2>/dev/null)
if [ "$BT_STATE" = "UP RUNNING" ]; then
    ok "hci0 已启动运行 (UP RUNNING)"
else
    fail "hci0 未启动"
fi

BT_ADDR=$($SSH 'hciconfig hci0 2>/dev/null | grep "BD Address" | awk "{print \$3}"' 2>/dev/null)
if [ -n "$BT_ADDR" ]; then
    ok "蓝牙地址: ${BT_ADDR}"
else
    warn "蓝牙地址未获取"
fi

BT_BUS=$($SSH 'hciconfig hci0 2>/dev/null | grep -o "Bus: UART"' 2>/dev/null)
[ "$BT_BUS" = "Bus: UART" ] && ok "总线类型: UART" || warn "总线类型未确认"

BT_FW=$($SSH 'journalctl -k --no-pager 2>/dev/null | grep "BCM.*Patch" | tail -1' 2>/dev/null)
if [ -n "$BT_FW" ]; then
    ok "蓝牙固件加载成功"
else
    warn "蓝牙固件加载状态未确认"
fi

HCI_UART=$($SSH 'lsmod 2>/dev/null | grep "^hci_uart "' 2>/dev/null)
[ -n "$HCI_UART" ] && ok "hci_uart 模块已加载" || fail "hci_uart 模块未加载"

# ── GPU ───────────────────────────────────────
hdr "GPU (Mali-G610 / panthor)"

PANTHOR=$($SSH 'lsmod 2>/dev/null | grep "^panthor "' 2>/dev/null)
if [ -n "$PANTHOR" ]; then
    ok "panthor 驱动已加载"
else
    fail "panthor 驱动未加载"
fi

GPU_CARD=$($SSH 'ls /sys/class/drm/ 2>/dev/null | grep -c "^card"' 2>/dev/null)
if [ "$GPU_CARD" -ge 2 ]; then
    ok "DRM 设备数量: ${GPU_CARD} (含 GPU card)"
else
    warn "DRM 设备数量: ${GPU_CARD}"
fi

GPU_FW=$($SSH 'journalctl -k --no-pager 2>/dev/null | grep "Initialized panthor" | tail -1' 2>/dev/null)
if [ -n "$GPU_FW" ]; then
    ok "GPU 固件加载成功 (panthor initialized)"
else
    fail "GPU 固件加载失败"
fi

# ── Ethernet ──────────────────────────────────
hdr "Ethernet"

ETH0_STATE=$($SSH 'ip -o link show eth0 2>/dev/null | grep -o "state UP"' 2>/dev/null)
if [ "$ETH0_STATE" = "state UP" ]; then
    ok "eth0: UP"
    ETH0_SPEED=$($SSH 'cat /sys/class/net/eth0/speed 2>/dev/null' 2>/dev/null)
    [ "$ETH0_SPEED" != "-1" ] && [ -n "$ETH0_SPEED" ] && ok "eth0 速率: ${ETH0_SPEED} Mbps" || warn "eth0 速率未知"
else
    fail "eth0: DOWN"
fi

ETH0_PHY=$($SSH 'ethtool -i eth0 2>/dev/null | grep "^driver:" | awk "{print \$2}"' 2>/dev/null)
[ -n "$ETH0_PHY" ] && ok "eth0 PHY 驱动: ${ETH0_PHY}" || warn "eth0 PHY 驱动未知"

ETH1_STATE=$($SSH 'ip -o link show eth1 2>/dev/null' 2>/dev/null)
if [ -n "$ETH1_STATE" ]; then
    ETH1_CARRIER=$($SSH 'cat /sys/class/net/eth1/carrier 2>/dev/null' 2>/dev/null)
    if [ "$ETH1_CARRIER" = "1" ]; then
        ok "eth1: 链接已连接"
    else
        warn "eth1: 接口存在但无网线连接"
    fi
else
    warn "eth1 接口不存在"
fi

# ── 存储 ──────────────────────────────────────
hdr "存储设备"

EMMC=$($SSH 'lsblk -d -n -o NAME,SIZE 2>/dev/null | grep "^mmcblk2 "' 2>/dev/null)
if [ -n "$EMMC" ]; then
    EMMC_SIZE=$(echo "$EMMC" | awk '{print $2}')
    ok "eMMC: ${EMMC_SIZE}"
else
    warn "eMMC 未检测到"
fi

SDCARD=$($SSH 'lsblk -d -n -o NAME,SIZE 2>/dev/null | grep "^mmcblk0 "' 2>/dev/null)
if [ -n "$SDCARD" ]; then
    SDCARD_SIZE=$(echo "$SDCARD" | awk '{print $2}')
    ok "SD 卡: ${SDCARD_SIZE}"
else
    warn "SD 卡未检测到"
fi

NVME=$($SSH 'lsblk -d -n -o NAME,SIZE 2>/dev/null | grep "^nvme"' 2>/dev/null)
if [ -n "$NVME" ]; then
    NVME_SIZE=$(echo "$NVME" | awk '{print $2}')
    ok "NVMe SSD: ${NVME_SIZE}"
else
    warn "NVMe SSD 未检测到"
fi

# ── 音频 ──────────────────────────────────────
hdr "音频 (ES8388)"

APLAY=$($SSH 'aplay -l 2>/dev/null | grep "card 0"' 2>/dev/null)
if [ -n "$APLAY" ]; then
    CARD_NAME=$(echo "$APLAY" | sed 's/.*: //; s/ \[.*//' | head -1)
    ok "声卡: ${CARD_NAME}"
else
    fail "声卡未检测到"
fi

APLAY_DEV=$($SSH 'aplay -l 2>/dev/null | grep "device 0"' 2>/dev/null)
[ -n "$APLAY_DEV" ] && ok "播放设备存在" || warn "播放设备未检测到"

AREC_DEV=$($SSH 'arecord -l 2>/dev/null | grep "device 0"' 2>/dev/null)
[ -n "$AREC_DEV" ] && ok "录音设备存在" || warn "录音设备未检测到"

# ── NPU ───────────────────────────────────────
hdr "NPU"

NPU_CORES=$($SSH 'journalctl -k --no-pager 2>/dev/null | grep -c "NPU core"' 2>/dev/null)
if [ "$NPU_CORES" -gt 0 ]; then
    ok "NPU 核心数: ${NPU_CORES}"
else
    warn "NPU 核心未检测到"
fi

# ── 视频编解码 ────────────────────────────────
hdr "视频编解码 (hantro/rga)"

VDEC=$($SSH 'ls /dev/video-dec* 2>/dev/null | wc -l' 2>/dev/null)
[ "$VDEC" -gt 0 ] && ok "视频解码设备: ${VDEC} 个" || warn "视频解码设备未检测到"

VENC=$($SSH 'ls /dev/video[0-9]* 2>/dev/null | wc -l' 2>/dev/null)
[ "$VENC" -gt 0 ] && ok "视频设备总数: ${VENC} 个 (含 RGA/VPU)" || warn "视频设备未检测到"

# ── USB ───────────────────────────────────────
hdr "USB"

USB_BUSES=$($SSH 'lsusb 2>/dev/null | wc -l' 2>/dev/null)
if [ "$USB_BUSES" -gt 0 ]; then
    ok "USB 设备总数: ${USB_BUSES}"
    USB_HUB=$($SSH 'lsusb 2>/dev/null | grep -i "hub"' 2>/dev/null)
    [ -n "$USB_HUB" ] && ok "USB Hub 已检测到" || true
else
    warn "USB 设备未检测到"
fi

# ── TypeC ─────────────────────────────────────
hdr "TypeC"

TYPEC_PORT=$($SSH 'ls /sys/class/typec/ 2>/dev/null | grep port' 2>/dev/null)
if [ -n "$TYPEC_PORT" ]; then
    ok "TypeC 端口: ${TYPEC_PORT}"
else
    warn "TypeC 端口未检测到"
fi

# ── RTC ───────────────────────────────────────
hdr "RTC (HYM8563)"

RTC=$($SSH 'cat /sys/class/rtc/rtc0/name 2>/dev/null' 2>/dev/null)
if [ -n "$RTC" ]; then
    ok "RTC: ${RTC}"
else
    fail "RTC 未检测到"
fi

RTC_IRQ=$($SSH 'cat /proc/interrupts 2>/dev/null | grep hym8563' 2>/dev/null)
if [ -n "$RTC_IRQ" ]; then
    IRQ1=$(echo "$RTC_IRQ" | awk '{print $2}')
    sleep 2
    RTC_IRQ2=$($SSH 'cat /proc/interrupts 2>/dev/null | grep hym8563' 2>/dev/null)
    IRQ2=$(echo "$RTC_IRQ2" | awk '{print $2}')
    IRQ_DELTA=$((IRQ2 - IRQ1))
    if [ "$IRQ_DELTA" -le 2 ]; then
        ok "RTC 中断稳定 (2秒内 +${IRQ_DELTA})"
    else
        fail "RTC 中断风暴! (2秒内 +${IRQ_DELTA})"
    fi
else
    warn "RTC 中断未注册"
fi

# ── PWM 风扇 ──────────────────────────────────
hdr "PWM 风扇"

FAN=$($SSH 'cat /sys/class/thermal/cooling_device0/type 2>/dev/null' 2>/dev/null)
if [ "$FAN" = "pwm-fan" ]; then
    ok "PWM 风扇已注册 (cooling_device0)"
    FAN_MAX=$($SSH 'cat /sys/class/thermal/cooling_device0/max_state 2>/dev/null' 2>/dev/null)
    ok "风扇调速级数: ${FAN_MAX}"
else
    warn "PWM 风扇未检测到"
fi

# ── 温度监控 ──────────────────────────────────
hdr "温度监控"

for zone in $($SSH 'ls /sys/class/thermal/ 2>/dev/null | grep thermal_zone' 2>/dev/null); do
    ZTYPE=$($SSH "cat /sys/class/thermal/${zone}/type 2>/dev/null" 2>/dev/null)
    ZTEMP=$($SSH "cat /sys/class/thermal/${zone}/temp 2>/dev/null" 2>/dev/null)
    if [ -n "$ZTEMP" ]; then
        TEMP_C=$(echo "scale=1; ${ZTEMP}/1000" | bc 2>/dev/null || echo "${ZTEMP}")
        ok "${ZTYPE}: ${TEMP_C}°C"
    fi
done

# ── PMIC ──────────────────────────────────────
hdr "PMIC / 电源管理"

PMIC=$($SSH 'journalctl -k --no-pager 2>/dev/null | grep -oE "rk80[0-9].*pwrkey|rk80[0-9].*spi" | head -1' 2>/dev/null)
if [ -n "$PMIC" ]; then
    ok "PMIC: ${PMIC}"
else
    warn "PMIC 信息未获取"
fi

# ── GPIO 扩展器 ───────────────────────────────
hdr "GPIO 扩展器"

PCA=$($SSH 'journalctl -k --no-pager 2>/dev/null | grep -o "pca953x 6-0020" | head -1' 2>/dev/null)
if [ -n "$PCA" ]; then
    ok "PCA953x GPIO 扩展器: I2C bus6 addr 0x20"
else
    warn "GPIO 扩展器未检测到"
fi

# ── LED ───────────────────────────────────────
hdr "LED"

LEDS=$($SSH 'ls /sys/class/leds/ 2>/dev/null' 2>/dev/null)
if [ -n "$LEDS" ]; then
    LED_COUNT=$(echo "$LEDS" | wc -l)
    ok "LED 数量: ${LED_COUNT} ($(echo $LEDS | tr '\n' ' '))"
else
    warn "LED 未检测到"
fi

# ── I2C 总线 ──────────────────────────────────
hdr "I2C 总线"

I2C_BUSES=$($SSH 'ls /sys/class/i2c-* 2>/dev/null | grep -c "i2c-"' 2>/dev/null)
if [ "$I2C_BUSES" -gt 0 ]; then
    ok "I2C 总线数量: ${I2C_BUSES}"
else
    warn "I2C 总线未检测到"
fi

# ── 显示 / HDMI ───────────────────────────────
hdr "显示 / HDMI"

HDMI_STATUS=$($SSH 'cat /sys/class/drm/card0-HDMI-A-1/status 2>/dev/null' 2>/dev/null)
if [ "$HDMI_STATUS" = "connected" ]; then
    ok "HDMI: 已连接显示器"
    HDMI_MODE=$($SSH 'cat /sys/class/drm/card0-HDMI-A-1/modes 2>/dev/null | head -1' 2>/dev/null)
    [ -n "$HDMI_MODE" ] && ok "HDMI 模式: ${HDMI_MODE}"
else
    warn "HDMI: 未连接显示器 (status=${HDMI_STATUS})"
fi

# ── 内核错误检查 ──────────────────────────────
hdr "内核错误检查"

FW_FAIL=$($SSH 'journalctl -k --no-pager 2>/dev/null | grep -c "Direct firmware load.*failed"' 2>/dev/null)
if [ "$FW_FAIL" -gt 0 ]; then
    warn "固件加载失败: ${FW_FAIL} 次"
    FW_DETAIL=$($SSH 'journalctl -k --no-pager 2>/dev/null | grep "Direct firmware load.*failed" | sed "s/.*load for //" | cut -d" " -f1' 2>/dev/null)
    for fw in $(echo "$FW_DETAIL" | sort -u); do
        warn "  缺少固件: ${fw}"
    done
else
    ok "无固件加载失败"
fi

PROBE_FAIL=$($SSH 'journalctl -k --no-pager 2>/dev/null | grep -c "probe.*failed"' 2>/dev/null)
if [ "$PROBE_FAIL" -gt 0 ]; then
    warn "驱动 probe 失败: ${PROBE_FAIL} 次"
else
    ok "无驱动 probe 失败"
fi

UNDERFLOW=$($SSH 'journalctl -k --no-pager 2>/dev/null | grep -c "underflow"' 2>/dev/null)
if [ "$UNDERFLOW" -gt 0 ]; then
    warn "PHY PM underflow 警告: ${UNDERFLOW} 次 (RK3588 已知问题)"
else
    ok "无 PM underflow 警告"
fi

DEFERRED=$($SSH 'cat /sys/kernel/debug/devices_deferred 2>/dev/null | wc -l' 2>/dev/null)
if [ "$DEFERRED" -gt 0 ]; then
    warn "延迟 probe 设备: ${DEFERRED} 个"
else
    ok "无延迟 probe 设备"
fi

# ── 串口 / UART ───────────────────────────────
hdr "串口 / UART"

TTYS=$($SSH 'ls /sys/class/tty/ 2>/dev/null | grep -c "^ttyS[0-9]*$"' 2>/dev/null)
[ "$TTYS" -gt 0 ] && ok "串口数量: ${TTYS}" || warn "串口未检测到"

UART9=$($SSH 'journalctl -k --no-pager 2>/dev/null | grep "febc0000.serial" | head -1' 2>/dev/null)
[ -n "$UART9" ] && ok "UART9 (febc0000) 已注册 (蓝牙使用)" || warn "UART9 未检测到"

# ── PCIe ──────────────────────────────────────
hdr "PCIe"

PCIE_DEVS=$($SSH 'lspci 2>/dev/null | wc -l' 2>/dev/null)
if [ "$PCIE_DEVS" -gt 0 ]; then
    ok "PCIe 设备数量: ${PCIE_DEVS}"
    PCIE_WIFI=$($SSH 'lspci 2>/dev/null | grep -i "network\|bcm43752"' 2>/dev/null)
    [ -n "$PCIE_WIFI" ] && ok "  WiFi: ${PCIE_WIFI}" || true
    PCIE_ETH=$($SSH 'lspci 2>/dev/null | grep -i "ethernet"' 2>/dev/null)
    [ -n "$PCIE_ETH" ] && ok "  Ethernet: ${PCIE_ETH}" || true
    PCIE_NVME=$($SSH 'lspci 2>/dev/null | grep -i "nvme\|ssd"' 2>/dev/null)
    [ -n "$PCIE_NVME" ] && ok "  NVMe: ${PCIE_NVME}" || true
else
    warn "PCIe 设备未检测到"
fi

# ── 汇总 ──────────────────────────────────────
echo -e "\n${BOLD}${CYAN}============================================${NC}"
echo -e "${BOLD}检测结果汇总${NC}"
echo -e "${GREEN}  通过: ${PASS}${NC}"
echo -e "${YELLOW}  警告: ${WARN}${NC}"
echo -e "${RED}  失败: ${FAIL}${NC}"
echo -e "${BOLD}${CYAN}============================================${NC}"

if [ "$FAIL" -eq 0 ]; then
    echo -e "${GREEN}${BOLD}所有关键外设检测通过！${NC}"
else
    echo -e "${RED}${BOLD}有 ${FAIL} 项检测失败，请检查上述详情。${NC}"
fi
