"""Port board wiring only; retain the target kernel's SoC resource bindings."""
import copy
import argparse
import json
import re
import struct
import subprocess
from pathlib import Path

from inspect_trees import HERE, read_tree, text


def cells(value):
    assert len(value) % 4 == 0
    return list(struct.unpack(">" + "I" * (len(value) // 4), value))


def packed(values):
    return struct.pack(">" + "I" * len(values), *values)


def descendants(tree, root):
    return [p for p in tree if p == root or p.startswith(root + "/")]


def format_prop(name, value):
    if not value:
        return name + ";"
    # Zero-only numeric cells must not be rendered as empty strings.
    if value.endswith(b"\0") and any(32 <= b < 127 for b in value) and all(
        b == 0 or 32 <= b < 127 for b in value
    ):
        strings = value[:-1].decode().split("\0")
        return name + " = " + ", ".join(json.dumps(s) for s in strings) + ";"
    if len(value) % 4 == 0:
        return name + " = <" + " ".join(hex(v) for v in cells(value)) + ">;"
    return name + " = [" + " ".join(f"{b:02x}" for b in value) + "];"


def emit_tree(tree, path, depth=0):
    lines = ["\t" * depth + ("/" if path == "/" else path.rsplit("/", 1)[1]) + " {"]
    for name, value in tree[path].items():
        if name not in ("phandle", "linux,phandle"):
            lines.append("\t" * (depth + 1) + format_prop(name, value))
        elif name == "phandle":
            lines.append("\t" * (depth + 1) + format_prop(name, value))
    for child in tree:
        if child != "/" and (child.rsplit("/", 1)[0] or "/") == path:
            lines.extend(emit_tree(tree, child, depth + 1))
    lines.append("\t" * depth + "};")
    return lines


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", type=Path, required=True)
    parser.add_argument("--factory", type=Path, required=True)
    parser.add_argument("--legacy-clock-header", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, default=HERE)
    parser.add_argument("--clock-header", type=Path, required=True)
    parser.add_argument("--base-name", default="rk3588-fnos-6.18.18-c1145.dtsi")
    parser.add_argument("--complete-display-graph", action="store_true")
    parser.add_argument("--isolate-vp2-pll", action="store_true")
    parser.add_argument("--legacy-mclkout", action="store_true",
                        help="Use the target vendor CRU's I2S MCLK output ID")
    args = parser.parse_args()
    output_dir = args.output_dir
    output_dir.mkdir(parents=True, exist_ok=True)
    factory_files = [args.factory]
    assert args.factory.is_file(), "Extract the original boot DTB first"
    factory = read_tree(factory_files[0])
    base = read_tree(args.base)
    result = copy.deepcopy(base)
    report = {"factory": factory_files[0].name, "changes": [], "disabled_optional": []}

    def clock_defines(path):
        return {
            name: int(value, 0)
            for name, value in re.findall(
                r"^#define\s+(\w+)\s+(0x[0-9a-fA-F]+|\d+)\s*$",
                path.read_text(), re.M
            )
        }
    old_clocks = clock_defines(args.legacy_clock_header)
    new_clocks = clock_defines(args.clock_header)
    clock_ids = {value: new_clocks[name] for name, value in old_clocks.items() if name in new_clocks}
    factory_handles = {
        cells(v["phandle"])[0]: p for p, v in factory.items() if "phandle" in v
    }
    next_handle = max(cells(v["phandle"])[0] for v in base.values() if "phandle" in v) + 1
    path_map = {p: p for p in factory if p in base}
    for path, props in factory.items():
        if path in path_map:
            continue
        if path.startswith("/uio@"):
            continue
        if "regulator-name" in props and not path.startswith("/i2c@"):
            candidates = [
                p for p, v in base.items()
                if v.get("regulator-name") == props["regulator-name"]
            ]
            if len(candidates) == 1:
                path_map[path] = candidates[0]
        if path not in path_map and "@" in path.rsplit("/", 1)[-1]:
            basename = path.rsplit("/", 1)[-1]
            parent = path.rsplit("/", 1)[0] or "/"
            mapped_parent = path_map.get(parent)
            candidates = [
                p for p in base if p.rsplit("/", 1)[-1] == basename
                and ((mapped_parent and (p.rsplit("/", 1)[0] or "/") == mapped_parent)
                     or "compatible" in props or "rockchip,pins" in props)
            ]
            if len(candidates) == 1:
                path_map[path] = candidates[0]
            elif path.count("/") == 1:
                address = basename.rsplit("@", 1)[-1]
                candidates = [
                    p for p in base if p.count("/") == 1 and "@" in p
                    and p.rsplit("@", 1)[-1] == address
                ]
                if len(candidates) == 1:
                    path_map[path] = candidates[0]

    # PMIC and CPU unit-address spellings differ between vendor kernel generations.
    for path in factory:
        if path in path_map:
            continue
        compatible = factory[path].get("compatible")
        reg = factory[path].get("reg")
        candidates = [
            p for p, v in base.items()
            if compatible and v.get("compatible") == compatible
            and reg is not None and v.get("reg") == reg
            and p.rsplit("/", 1)[0] == path.rsplit("/", 1)[0]
        ]
        if len(candidates) == 1:
            path_map[path] = candidates[0]
    for source in factory:
        if "/rk806single@0" in source:
            candidate = source.replace("/rk806single@0", "/pmic@0")
            if candidate in base:
                path_map[source] = candidate
            for index in (1, 2, 3):
                if source.endswith(f"/rk806_dvs{index}_null"):
                    candidate = f"/spi@feb20000/pmic@0/dvs{index}-null-pins"
                    if candidate in base:
                        path_map[source] = candidate
                if source.endswith(f"/rk806_dvs{index}_slp"):
                    candidate = f"/spi@feb20000/pmic@0/dvs{index}-sleep-pins"
                    # Newer PMIC bindings permit a board-specific sleep state
                    # that the target base has not defined yet.
                    if candidate in base or "/spi@feb20000/pmic@0" in base:
                        path_map[source] = candidate

    # A regulator name identifies its rail, but never its physical I2C bus.
    # Relocate the donor resource while keeping its phandle for SoC consumers.
    moved_regulators = []
    for source, props in factory.items():
        if not source.startswith("/i2c@") or "regulator-name" not in props:
            continue
        candidates = [p for p, v in base.items()
                      if p.startswith("/i2c@") and v.get("regulator-name") == props["regulator-name"]]
        if len(candidates) != 1:
            continue
        donor = candidates[0]
        destination = source.rsplit("/", 1)[0] + "/" + donor.rsplit("/", 1)[1]
        path_map[source] = destination
        moved_regulators.append(source)
        if donor != destination:
            for path in descendants(base, donor):
                result[destination + path[len(donor):]] = result.pop(path)
            report["changes"].append({"path": destination, "property": "physical-i2c-bus"})

    # Map children after their renamed parent (for example usb2-phy/host-port).
    # These children have no unit address and cannot use the address fallback.
    mapped_children = True
    while mapped_children:
        mapped_children = False
        for source in factory:
            if source in path_map or source == "/":
                continue
            parent, name = source.rsplit("/", 1)
            parent = parent or "/"
            if parent not in path_map:
                continue
            candidate = path_map[parent].rstrip("/") + "/" + name
            if candidate in base:
                path_map[source] = candidate
                mapped_children = True
            elif candidate in result:
                path_map[source] = candidate
                mapped_children = True

    # Vendor PHY children became typed PHY arguments in the target kernel.
    for phy in ("fed80000", "fed90000"):
        for port in ("u3-port", "dp-port"):
            path_map[f"/phy@{phy}/{port}"] = f"/phy@{phy}"
    path_map["/usbdrd3_0/usb@fc000000/port/endpoint@0"] = "/usb@fc000000/port/endpoint"

    def ensure(source):
        nonlocal next_handle
        was_mapped = source in path_map
        destination = path_map.get(source, source)
        path_map[source] = destination
        if destination not in result:
            parent = source.rsplit("/", 1)[0] or "/"
            if not was_mapped:
                if parent not in path_map:
                    ensure(parent)
                destination = (path_map[parent].rstrip("/") + "/" + source.rsplit("/", 1)[1])
            path_map[source] = destination
            result.setdefault(destination, {})
        if "phandle" not in result[destination]:
            result[destination]["phandle"] = packed([next_handle])
            next_handle += 1
        return destination

    def reference(handle):
        source = factory_handles[handle]
        destination = ensure(source)
        return cells(result[destination]["phandle"])[0]

    first_reference = {
        "interrupt-parent", "phy-handle", "mmc-pwrseq", "ddc-i2c-bus",
        "sound-dai", "rockchip,cpu", "rockchip,codec", "remote-endpoint", "extcon",
    }

    def translate(name, value):
        if name in ("clocks", "assigned-clocks", "assigned-clock-parents"):
            values = cells(value)
            i = 0
            while i < len(values):
                provider_path = factory_handles[values[i]]
                provider = factory[provider_path]
                count = cells(provider.get("#clock-cells", packed([0])))[0]
                if provider_path == "/clocks/mclkout-i2s0@fd58c318":
                    values[i:i + 1] = [reference(next(h for h,p in factory_handles.items()
                        if p == "/clock-controller@fd7c0000")), new_clocks["I2S0_8CH_MCLKOUT_TO_IO"]]
                    i += 2
                    continue
                values[i] = reference(values[i])
                if provider_path == "/clock-controller@fd7c0000":
                    values[i + 1] = clock_ids[values[i + 1]]
                i += 1 + count
            return packed(values)
        if name == "phys":
            values = cells(value)
            translated = []
            i = 0
            while i < len(values):
                provider_path = factory_handles[values[i]]
                provider = factory[provider_path]
                count = cells(provider.get("#phy-cells", packed([0])))[0]
                translated.append(reference(values[i]))
                if provider_path.endswith("/u3-port"):
                    translated.append(4)
                elif provider_path.endswith("/dp-port"):
                    translated.append(6)
                elif (count == 0 and
                      cells(result[path_map[provider_path]].get("#phy-cells", packed([0])))[0] == 1 and
                      b"dcphy" in provider.get("compatible", b"")):
                    # Target D/C-PHY bindings require the physical PHY type.
                    # The vendor tree's DSI consumer implicitly selected D-PHY.
                    translated.append(10)
                else:
                    translated.extend(values[i+1:i+1+count])
                i += 1 + count
            return packed(translated)
        if name == "pwms":
            values = cells(value)
            values[0] = reference(values[0])
            return packed(values)
        if name == "io-channels":
            values = cells(value)
            i = 0
            while i < len(values):
                provider_path = factory_handles[values[i]]
                provider = factory[provider_path]
                count = cells(provider.get("#io-channel-cells", packed([0])))[0]
                values[i] = reference(values[i])
                i += 1 + count
            return packed(values)
        if name.endswith("-supply") or name in first_reference:
            values = cells(value)
            values[0] = reference(values[0])
            return packed(values)
        if name.startswith("pinctrl-") and name != "pinctrl-names":
            return packed([reference(v) for v in cells(value)])
        if name in ("gpio", "gpios") or name.endswith(("-gpios", "-gpio", "_gpio", "_irq")):
            values = cells(value)
            values[0] = reference(values[0])
            return packed(values)
        if name == "rockchip,pins":
            values = cells(value)
            for i in range(3, len(values), 4):
                values[i] = reference(values[i])
            return packed(values)
        return value

    # Root board rails and pinmux data come exclusively from the original board.
    copied = []
    for path, props in factory.items():
        if path.count("/") == 1 and (
            props.get("compatible") in (
                b"regulator-fixed\0", b"mmc-pwrseq-simple\0", b"gpio-leds\0", b"pwm-fan\0"
            )
        ):
            copied.extend(descendants(factory, path))
    copied.extend(p for p in factory if p.startswith("/pinctrl/") and "/gpio@" not in p)
    copied.extend(
        p for p in factory if "/pinctrl_rk806/rk806_dvs" in p and p.endswith("_slp")
        and p in path_map
    )
    copied.extend(p for p in factory if "/pinctrl_rk806/" in p and
                  factory[p].get("function") and p.endswith("_pwrdn"))
    for path, props in factory.items():
        if path.startswith("/i2c@") and (
            props.get("compatible") in (b"everest,es8388\0everest,es8323\0", b"haoyu,hym8563\0", b"fcs,fusb302\0")
        ):
            copied.extend(descendants(factory, path))
        if path in ("/es8388-sound", "/adc-keys"):
            copied.extend(descendants(factory, path))
    # Keep target-kernel regulator bindings, including RK8603's RK8602 fallback
    # and fcs,suspend-voltage-selector. Only relocate and translate wiring.
    for path in copied:
        ensure(path)
    for source in {p for p in copied if p.count("/") == 1}:
        destination = path_map[source]
        wanted = {path_map[p] for p in descendants(factory, source) if p in path_map}
        for old_path in list(result):
            if old_path.startswith(destination + "/") and old_path not in wanted:
                del result[old_path]
    for path in copied:
        destination = ensure(path)
        handle = result[destination]["phandle"]
        result[destination] = {
            n: translate(n, v) for n, v in factory[path].items()
            if n not in ("phandle", "linux,phandle")
        }
        result[destination]["phandle"] = handle

    copied_roots = {path_map[p] for p in copied if p.count("/") == 1}
    # Stop T6-only GPIO consumers before introducing the new board's wiring.
    for path, props in base.items():
        if path.count("/") == 1 and props.get("compatible") in (
            b"regulator-fixed\0", b"gpio-leds\0", b"gpio-ir-receiver\0",
            b"adc-keys\0", b"pwm-fan\0"
        ) and path not in copied_roots:
            result[path]["status"] = b"disabled\0"

    board_names = {
        "status", "pinctrl-names", "phy-mode", "clock_in_out", "tx_delay", "rx_delay",
        "reset-gpios", "gpio", "gpios", "enable-active-high", "enable-active-low",
        "snps,reset-gpio", "snps,reset-active-low", "snps,reset-delays-us",
        "bus-width", "non-removable", "disable-wp", "broken-cd",
        "cap-sd-highspeed", "cap-mmc-highspeed", "sd-uhs-sdr104", "mmc-hs200-1_8v",
        "mmc-hs400-1_8v", "mmc-hs400-enhanced-strobe", "keep-power-in-suspend",
        "cap-sdio-irq", "supports-sdio", "mmc-pwrseq", "cd-gpios", "no-sd", "no-mmc", "no-sdio",
        "max-frequency", "post-power-on-delay-ms", "reset-assert-us",
        "reset-deassert-us", "snps,disable-autoneg", "phy-handle",
        "num-lanes", "max-link-speed", "phys", "data-lanes", "rockchip,pcie30-phymode",
        "dr_mode", "maximum-speed", "phy-supply", "phy-reset-gpios",
        "rockchip,trcm-sync-tx-only", "rockchip,trcm-sync-rx-only",
        "rockchip,clk-trcm", "rockchip,playback-only", "rockchip,capture-only",
        "hpd-trigger-level",
    }
    skip_status_prefixes = (
        "/gpu", "/npu", "/rga", "/vpu", "/rkv", "/venc", "/vdpu", "/vepu",
        "/dmc", "/iommu", "/display-subsystem", "/vop",
    )
    for source, destination in list(path_map.items()):
        if (source not in factory or (destination not in base and source not in moved_regulators)
            or source.startswith(("/pinctrl", "/uio@"))):
            continue
        if source.startswith("/__"):
            continue
        props = factory[source]
        if source.startswith(("/ethernet@", "/phy@", "/hdmirx-controller@", "/pcie@", "/mmc@")):
            for name in list(result[destination]):
                if (name.endswith(("-gpios", "-gpio", "_gpio", "_irq")) or
                    name.endswith("-supply") or
                    name in ("mode-switch", "orientation-switch", "svid",
                             "snps,reset-active-low", "rx_delay")) and name not in props:
                    # The NVR reference tree does not describe the local
                    # USB-DP mux, but the RK3588 USB-DP PHY base tree does.
                    # Keep it for this board or the Type-C DP altmode driver
                    # has no mux to bind and can never activate the partner.
                    if (source == "/phy@fed80000" and
                        name == "mode-switch"):
                        continue
                    del result[destination][name]
        if source.startswith("/pcie@") and "pinctrl-0" not in props:
            result[destination].pop("pinctrl-0", None)
            result[destination].pop("pinctrl-names", None)
        if "usb2-phy@" in source and source.endswith(("/host-port", "/otg-port")):
            if "phy-supply" not in props:
                result[destination].pop("phy-supply", None)
        for name, value in props.items():
            is_board = (
                name in board_names or name.endswith("-supply")
                or name.startswith("pinctrl-") or name.startswith("regulator-")
                or name.endswith(("-gpios", "-gpio", "_gpio", "_irq"))
                or name in ("rockchip,dp-lane-mux", "orientation-switch", "svid")
                or (source.startswith("/i2c@") and source.count("/") == 2
                    and name in ("interrupt-parent", "interrupts"))
            )
            if not is_board:
                continue
            if name == "status" and source.startswith(skip_status_prefixes):
                continue
            # No generic bus rate overrides from the old kernel.
            if name == "max-frequency" and not source.startswith("/mmc@"):
                continue
            target_name = "snps,reset-gpios" if name == "snps,reset-gpio" else name
            translated = translate(name, value)
            if result[destination].get(target_name) != translated:
                result[destination][target_name] = translated
                report["changes"].append({"path": destination, "property": target_name})

    # A multimedia block and its IOMMU must be enabled as one resource group.
    # The target reference board disables IEP's IOMMU, while the I3588 factory
    # enables IEP. Keep DMA translation available for the enabled consumer.
    # This alone does not prove that the vendor driver's DMA allocations work.
    result_handles = {cells(v["phandle"])[0]: p for p, v in result.items()
                      if "phandle" in v}
    for path, props in list(result.items()):
        if props.get("status", b"okay\0") == b"disabled\0" or "iommus" not in props:
            continue
        values = cells(props["iommus"])
        index = 0
        while index < len(values):
            provider = result_handles[values[index]]
            result[provider]["status"] = b"okay\0"
            index += 1 + cells(result[provider].get("#iommu-cells", packed([0])))[0]

    # Restore the original board's encoder OPP only where the target vendor
    # binding has no OPP at all. Keep target resource IDs; translate the
    # leakage-cell and GRF references instead of copying raw phandles.
    for source, props in factory.items():
        destination = path_map.get(source)
        if (not destination or destination not in result or
            props.get("compatible") != b"rockchip,rkv-encoder-v2-core\0" or
            "operating-points-v2" not in props or
            "operating-points-v2" in result[destination]):
            continue
        table = factory_handles[cells(props["operating-points-v2"])[0]]
        for node in descendants(factory, table):
            ensure(node)
        for node in descendants(factory, table):
            target = path_map[node]
            handle = result[target]["phandle"]
            result[target] = {}
            for name, value in factory[node].items():
                if name in ("phandle", "linux,phandle"):
                    continue
                if name in ("nvmem-cells", "rockchip,grf"):
                    value = packed([reference(v) for v in cells(value)])
                else:
                    value = translate(name, value)
                result[target][name] = value
            result[target]["phandle"] = handle
        result[destination]["operating-points-v2"] = result[path_map[table]]["phandle"]

    # The target tree exports a complete dual-rail encoder OPP table but leaves
    # both encoder cores disconnected from it. Use that target binding rather
    # than importing old clock/regulator IDs from the factory kernel.
    encoders = [p for p, v in result.items()
                if v.get("compatible") == b"rockchip,rkv-encoder-v2-core\0"
                and v.get("status", b"okay\0") != b"disabled\0"
                and "operating-points-v2" not in v]
    if encoders and "/venc-opp-table" in result:
        table = ensure("/venc-opp-table")
        for path in encoders:
            result[path]["operating-points-v2"] = result[table]["phandle"]
            report["changes"].append({"path": path, "property": "operating-points-v2"})

    # Correct the typo shared by the vendor trees; the IOMMU driver requests
    # clock-names, not lock-names.
    for props in result.values():
        if "#iommu-cells" in props and "lock-names" in props and "clock-names" not in props:
            props["clock-names"] = props.pop("lock-names")

    # The upstream SoC tree leaves DP input endpoints to the board DTS.
    # Preserve the original board's active VOP route when the reference board
    # has no DP graph; do not enable any extra output or use a guessed route.
    if args.complete_display_graph:
        # TCPM needs local DP capabilities as well as the remote partner's
        # discovered modes. The vendor u32 SVID is not the upstream u16 binding.
        # Resolve vendor and target node names through the existing mapping.
        connector_source = "/i2c@fec80000/fusb302@22/connector"
        connector = path_map.get(connector_source,
                                 "/i2c@fec80000/usb-typec@22/connector")
        if connector not in result:
            raise RuntimeError("Missing USB-C connector for local DP capability")
        # Normalize the vendor TCPM graph to the target connector binding:
        # port0 USB2 role switch, port1 USB3 orientation, port2 DP altmode.
        usb_endpoint = "/usb@fc000000/port/endpoint"
        phy_endpoints = ["/phy@fed80000/port/endpoint@0",
                         "/phy@fed80000/port/endpoint@1"]
        for index, peer in enumerate([usb_endpoint, *phy_endpoints]):
            endpoint = connector + f"/ports/port@{index}/endpoint"
            result[endpoint]["remote-endpoint"] = result[peer]["phandle"]
            result[peer]["remote-endpoint"] = result[endpoint]["phandle"]
        controller_ports = connector.rsplit("/", 1)[0] + "/ports"
        for path in descendants(result, controller_ports):
            del result[path]
        result[connector].update({
            "data-role": b"dual\0",
        })
        result["/phy@fed80000"]["mode-switch"] = b""
        result["/phy@fed80000"]["orientation-switch"] = b""
        altmodes = connector + "/altmodes"
        for path in list(result):
            if path.startswith(altmodes + "/"):
                del result[path]
        result[altmodes] = {}
        result[altmodes + "/displayport"] = {
            "svid": struct.pack(">H", 0xff01),
            "vdo": packed([0x00001c46]),
        }
        report["changes"].append({"path": altmodes, "property": "displayport"})
        if args.clock_header is None:
            raise RuntimeError("Complete display graph needs target kernel headers")
        vop_header = args.clock_header.parent.parent / "soc/rockchip,vop2.h"
        vop_ids = clock_defines(vop_header)
        for source, props in factory.items():
            if source.count("/") != 1 or not source.startswith("/dp@"):
                continue
            destination = path_map.get(source, source)
            if result.get(destination, {}).get("status") != b"okay\0":
                continue
            input_port = destination + "/ports/port@0"
            if input_port not in result or any(
                p.startswith(input_port + "/") and "remote-endpoint" in v
                for p, v in result.items()
            ):
                continue
            active = [
                (p, v) for p, v in factory.items()
                if p.startswith(source + "/ports/port@0/")
                and v.get("status", b"okay\0") == b"okay\0"
                and "remote-endpoint" in v
            ]
            if len(active) != 1:
                raise RuntimeError(f"Expected one original active DP input: {source}")
            endpoint, values = active[0]
            vop_source = factory_handles[cells(values["remote-endpoint"])[0]]
            vop_target = ensure(vop_source)
            dp_index = {"/dp@fde50000": 0, "/dp@fde60000": 1}[destination]
            endpoint_id = vop_ids[f"ROCKCHIP_VOP2_EP_DP{dp_index}"]
            # BSP endpoint IDs are not the target kernel's interface IDs.
            renamed = vop_target.rsplit("/", 1)[0] + f"/endpoint@{endpoint_id:x}"
            if renamed != vop_target:
                if renamed in result:
                    raise RuntimeError(f"DP endpoint collision: {renamed}")
                result[renamed] = result.pop(vop_target)
                vop_target = renamed
            result[vop_target]["reg"] = packed([endpoint_id])
            dp_target = input_port + "/endpoint"
            result[dp_target] = {"phandle": packed([next_handle])}
            next_handle += 1
            result[dp_target]["remote-endpoint"] = result[vop_target]["phandle"]
            result[vop_target]["remote-endpoint"] = result[dp_target]["phandle"]
            report["changes"].append({"path": dp_target, "property": "remote-endpoint"})
            report["changes"].append({"path": vop_target, "property": "reg",
                                      "interface": f"DP{dp_index}", "value": endpoint_id})

    if args.isolate_vp2_pll:
        if not args.complete_display_graph:
            raise RuntimeError("VP2 PLL isolation requires the complete DP graph")
        vop = result["/vop@fdd90000"]
        cru = cells(vop["clocks"])[0]
        # c1145 propagates VP2 rate changes to its parent. Never retune GPLL:
        # DP AUX and unrelated peripherals also derive their clocks from it.
        vop["assigned-clocks"] = packed([cru, new_clocks["DCLK_VOP2_SRC"]])
        vop["assigned-clock-parents"] = packed([cru, new_clocks["PLL_V0PLL"]])
        report["changes"].append({"path": "/vop@fdd90000",
                                  "property": "assigned-clock-parents",
                                  "parent": "PLL_V0PLL"})

    # Ethernet PHY parameters are board-specific, not inherited from the T6.
    for source in factory:
        if "/ethernet@" in source and "/mdio/phy@" in source:
            destination = ensure(source)
            handle = result[destination]["phandle"]
            result[destination] = {"phandle": handle}
            for name, value in factory[source].items():
                if name not in ("phandle", "linux,phandle", "interrupts"):
                    result[destination][name] = translate(name, value)

    # The donor PCIe pinctrl groups describe NanoPC-T6 reset wiring. I3588 uses
    # GPIO descriptors from the factory tree; retaining both pinctrl groups
    # can claim the same GPIO for two independent PCIe hosts.
    for path in ("/pcie@fe180000", "/pcie@fe190000"):
        result[path].pop("pinctrl-0", None)
        result[path].pop("pinctrl-names", None)

    for path, props in base.items():
        if (path.startswith("/i2c@") and path.count("/") == 2
            and "compatible" in props and path not in path_map.values()):
            if path in result:
                result[path]["status"] = b"disabled\0"

    # Preserve target-kernel USB wrappers and resources; use the board's host roles.
    for path in result:
        if path.startswith("/usb") and path.endswith(("usb@fc000000", "usb@fc400000")):
            source_props = factory.get(path, {})
            if "dr_mode" in source_props:
                result[path]["dr_mode"] = source_props["dr_mode"]

    # USB-root needs an initramfs: the stock target kernel has USBDP PHY as a module.
    # eMMC remains available as hardware but CQE is not enabled in this port.
    for path in result:
        if path.startswith("/mmc@"):
            result[path].pop("supports-cqe", None)
        if path.startswith("/ethernet@"):
            result[path].pop("local-mac-address", None)
            result[path].pop("mac-address", None)

    result["/"]["model"] = b"9Tripod I3588\0"
    result["/"]["compatible"] = b"9tripod,i3588\0rockchip,rk3588\0"
    # PCIe-only reference boards have no GMAC alias for U-Boot MAC fixups.
    for name, value in factory.get("/aliases", {}).items():
        if name.startswith("ethernet"):
            source_path = text(value)
            mapped_path = path_map.get(source_path, source_path)
            if mapped_path in result:
                result.setdefault("/aliases", {})[name] = mapped_path.encode() + b"\0"
    result.setdefault("/chosen", {})
    result["/chosen"] = {"stdout-path": b"serial2:1500000n8\0"}
    # Android uses a separate debugger; fnOS needs the ordinary serial console.
    result["/serial@feb50000"]["status"] = b"okay\0"
    result["/serial@feb50000"]["pinctrl-names"] = b"default\0"
    result["/serial@feb50000"]["pinctrl-0"] = translate(
        "pinctrl-0", factory["/fiq-debugger"]["pinctrl-0"])
    result["/serial@feb50000"].pop("dmas", None)
    result["/serial@feb50000"].pop("dma-names", None)
    # Do not carry T6 identity, IR receiver, camera GPIOs or modem power outputs.
    for path in result:
        if path.startswith(("/ir-receiver", "/regulator-vdd-4g", "/wireless-")):
            result[path]["status"] = b"disabled\0"
    if "/sound" in result and "/sound" not in factory:
        result["/sound"]["status"] = b"disabled\0"
    monitor = "/rockchip-system-monitor"
    if monitor in factory and monitor in result:
        result[monitor]["status"] = factory[monitor].get("status", b"okay\0")
    for path in list(result):
        if path.startswith("/__symbols__"):
            del result[path]

    # BV2 schematic: Q10 switches J17 fan ground from GPIO3_B2.
    # The factory's PWM fan node does not match this two-wire connector.
    for path in list(result):
        if path.startswith("/pwm-fan"):
            del result[path]
    gpio3 = cells(result["/pinctrl/gpio@fec40000"]["phandle"])[0]
    fan_handle = next_handle
    next_handle += 1
    result["/gpio-fan"] = {
        "compatible": b"gpio-fan\0", "gpios": packed([gpio3, 10, 0]),
        "gpio-fan,speed-map": packed([0, 0, 5000, 1]),
        "#cooling-cells": packed([2]), "phandle": packed([fan_handle]),
    }
    zone = "/thermal-zones/package-thermal"
    for path in list(result):
        if path.startswith(zone + "/cooling-maps/"):
            del result[path]
    result[zone]["polling-delay"] = packed([1000])
    trip_handle = next_handle
    next_handle += 1
    result[zone + "/trips/i3588-fan"] = {
        "temperature": packed([55000]), "hysteresis": packed([5000]),
        "type": b"active\0", "phandle": packed([trip_handle]),
    }
    result[zone + "/cooling-maps"] = {}
    result[zone + "/cooling-maps/i3588-fan"] = {
        "trip": packed([trip_handle]), "cooling-device": packed([fan_handle, 0, 1]),
    }
    for key in ("menu-key", "back-key"):
        result.pop("/adc-keys/" + key, None)
    # AP6275P PCIe Wi-Fi power. PERST is separately owned by PCIe1L0.
    gpio0=cells(result["/pinctrl/gpio@fd8a0000"]["phandle"])[0]
    # The connected core uses CV1 wiring, confirmed against the official CV1
    # image and live ID reads (0x8a/0x01 at both 0x42 and 0x43). SDA=GPIO0_D1,
    # SCL=GPIO0_D2 is reversed relative to hardware I2C0_M2 on the CV2 core.
    cpu_bus = "/i2c-cpu"
    result["/i2c@fd880000"]["status"] = b"disabled\0"
    result[cpu_bus] = {
        "compatible": b"i2c-gpio\0", "#address-cells": packed([1]),
        "#size-cells": packed([0]), "sda-gpios": packed([gpio0, 25, 6]),
        "scl-gpios": packed([gpio0, 26, 6]),
        "i2c-gpio,delay-us": packed([2]), "status": b"okay\0",
    }
    cpu_regulators = [path for path, props in result.items()
                      if path.startswith("/i2c@fd880000/")
                      and "regulator-name" in props]
    for source in cpu_regulators:
        for path in descendants(result, source):
            result[cpu_bus + path[len("/i2c@fd880000"):]] = result.pop(path)
    report["changes"].append({"path": cpu_bus,
        "property": "verified-cv1-cpu-regulator-gpio-bus"})
    wifi_handle=next_handle
    next_handle+=1
    result["/regulator-wifi"]={"compatible":b"regulator-fixed\0",
        "regulator-name":b"vcc_wifi\0","regulator-min-microvolt":packed([3300000]),
        "regulator-max-microvolt":packed([3300000]),"gpio":packed([gpio0,20,0]),
        "enable-active-high":b"","regulator-boot-on":b"","regulator-always-on":b"",
        "startup-delay-us":packed([200000]),"phandle":packed([wifi_handle])}
    # Broadcom serdev bindings replace the vendor wireless-bluetooth shim.
    uart="/serial@febc0000"
    result[uart]["status"]=b"okay\0"
    result[uart]["uart-has-rtscts"] = b""
    result[uart]["pinctrl-0"] = packed([
        cells(result["/pinctrl/uart9/" + group]["phandle"])[0]
        for group in ("uart9m0-xfer", "uart9m0-ctsn", "uart9m0-rtsn")
    ])
    for key in ("dmas","dma-names"):
        result[uart].pop(key,None)
    old=factory["/wireless-bluetooth"]
    result[uart+"/bluetooth"]={"compatible":b"brcm,bcm43438-bt\0",
        "shutdown-gpios":translate("shutdown-gpios",old["BT,reset_gpio"]),
        "device-wakeup-gpios":translate("device-wakeup-gpios",old["BT,wake_gpio"]),
        "host-wakeup-gpios":translate("host-wakeup-gpios",old["BT,wake_host_irq"]),
        "max-speed":packed([1500000])}
    # The RTC CLKOUT feeds the module's 32KOUT_WIFI input. Its driver
    # disables CLKOUT when registering the provider; both radio functions
    # must acquire the real clock rather than inherit a factory boot state.
    rtc = path_map["/i2c@fec80000/hym8563@51"]
    rtc_clock = result[rtc]["phandle"]
    result[uart+"/bluetooth"]["clocks"] = rtc_clock
    result[uart+"/bluetooth"]["clock-names"] = b"lpo\0"
    radio_port = "/pcie@fe170000/pcie@0,0"
    for path in list(result):
        if path == radio_port or path.startswith(radio_port + "/"):
            del result[path]
    result[radio_port] = {"reg": packed([0, 0, 0, 0, 0]),
                         "#address-cells": packed([3]),
                         "#size-cells": packed([2]), "ranges": b""}
    result[radio_port+"/wifi@0,0"] = {
        "compatible": b"pci14e4,449d\0", "reg": packed([0, 0, 0, 0, 0]),
        "clocks": rtc_clock, "clock-names": b"lpo\0",
    }
    report["changes"].append({"path": radio_port+"/wifi@0,0",
                              "property": "rtc-32768-hz-lpo-clock"})
    if args.legacy_mclkout:
        # The distribution's vendor CRU predates the extra TO_IO clock ID
        # in newer binding headers. Its exported output is I2S0_8CH_MCLKOUT.
        codec = "/i2c@fec90000/es8388@11"
        cru = result["/clock-controller@fd7c0000"]["phandle"]
        result[codec]["clocks"] = cru + packed([new_clocks["I2S0_8CH_MCLKOUT"]])
    codec = "/i2c@fec90000/es8388@11"
    # HDMI/display initialization retunes AUPLL after the codec assigns its
    # MCLK. Use stable GPLL for I2S0 so that this cannot invalidate the codec
    # rate before simple-card probes (or while a PCM stream is running).
    cru = result["/clock-controller@fd7c0000"]["phandle"]
    result["/i2s@fe470000"]["assigned-clocks"] = b"".join(
        cru + packed([new_clocks[name]])
        for name in ("CLK_I2S0_8CH_TX_SRC", "CLK_I2S0_8CH_RX_SRC"))
    result["/i2s@fe470000"]["assigned-clock-parents"] = (
        cru + packed([new_clocks["PLL_GPLL"]])) * 2
    result[codec]["compatible"] = b"everest,es8388\0everest,es8328\0"
    rails = {}
    for voltage, name in ((1800000, "vcc_1v8_s3"), (3300000, "vcc_3v3_s3")):
        rail = next(v for v in result.values()
                    if v.get("regulator-name") == name.encode() + b"\0")
        if "phandle" not in rail:
            rail["phandle"] = packed([next_handle])
            next_handle += 1
        rails[voltage] = rail["phandle"]
    # BV2 page 15: codec DVDD/PVDD use 1.8 V; AVDD/HPVDD use 3.3 V.
    for supply, voltage in (("DVDD", 1800000), ("PVDD", 1800000),
                            ("AVDD", 3300000), ("HPVDD", 3300000)):
        result[codec][supply + "-supply"] = rails[voltage]
    sound = "/es8388-sound"
    original_sound = factory[sound]
    result[sound] = {
        "compatible": b"simple-audio-card\0",
        "simple-audio-card,name": b"I3588-ES8388\0",
        "simple-audio-card,format": b"i2s\0",
        "simple-audio-card,mclk-fs": packed([256]),
        # simple-card's GPIO jack helper controls the exact pin "Headphones".
        "simple-audio-card,widgets": b"Headphone\0Headphones\0Speaker\0Speaker\0Line\0Line In\0Microphone\0Main Mic\0",
        "simple-audio-card,routing": (
            b"Headphones\0LOUT1\0Headphones\0ROUT1\0"
            b"Speaker\0LOUT2\0Speaker\0ROUT2\0"
            b"LINPUT1\0Line In\0RINPUT1\0Line In\0"
            b"LINPUT2\0Main Mic\0RINPUT2\0Main Mic\0"),
        "simple-audio-card,hp-det-gpios": translate(
            "hp-det-gpios", original_sound["hp-det-gpio"]),
        "status": b"okay\0",
    }
    for path in list(result):
        if path.startswith(sound + "/"):
            del result[path]
    result[sound + "/simple-audio-card,cpu"] = {
        "sound-dai": translate("sound-dai", original_sound["rockchip,cpu"]),
    }
    result[sound + "/simple-audio-card,codec"] = {
        "sound-dai": result[codec]["phandle"],
    }
    # SC7A20 uses the upstream ST IIO family driver, with its real 0x11 ID.
    # A missing IRQ still permits direct IIO raw reads; do not invent a GPIO.
    sensor = "/i2c@feac0000/accelerometer@18"
    result[sensor] = {
        "compatible": b"silan,sc7a20\0", "reg": packed([0x18]),
        "vdd-supply": rails[1800000], "vddio-supply": rails[1800000],
        "mount-matrix": b"\0".join(x.encode() for x in ("1", "0", "0", "0", "1", "0", "0", "0", "1")) + b"\0",
        "status": b"okay\0",
    }
    # BV2 HDMIIRX_DET_L is GPIO1_D5, active low. The mainline receiver
    # requests hpd-gpios; the vendor spelling is silently ignored by it.
    receiver = "/hdmi_receiver@fdee0000"
    if receiver in result:
        result[receiver]["hpd-gpios"] = result[receiver].pop("hdmirx-det-gpios")
        trigger = result[receiver].pop("hpd-trigger-level", packed([1]))
        if cells(trigger) == [0]:
            result[receiver]["hpd-is-active-low"] = b""
        else:
            result[receiver].pop("hpd-is-active-low", None)
    # Unspecified external panels cannot join the default DRM component graph.
    # Keep the DSI hardware available for a panel-specific overlay; HDMI/DP
    # must still register when no panel has been configured on either header.
    for path in ("/dsi@fde20000", "/dsi@fde30000"):
        if path in result and not any(
            p.startswith(path + "/") and ("panel" in p or "bridge" in p)
            for p in result
        ):
            result[path]["status"] = b"disabled\0"
            report["disabled_optional"].append({"path": path,
                "reason": "external panel requires a panel-specific overlay"})
    # Initial PCIe x4 aggregate mode preserves independent Wi-Fi and Ethernet lanes.
    result["/phy@fee80000"]["data-lanes"]=packed([1,1,1,1])
    result["/phy@fee80000"].pop("rockchip,pcie30-phymode",None)
    result["/pcie@fe150000"]["num-lanes"]=packed([4])
    result["/pcie@fe160000"]["status"]=b"disabled\0"
    # BV2 CON2 uses the Type-C1 USB-DP PHY. COMBPHY2 is wired to
    # RTL8111; the factory's third USB3 controller cannot share that lane.
    for path in ("/usb@fcd00000", "/usbhost3_0", "/usbhost3_0/usb@fcd00000"):
        if path in result:
            result[path]["status"] = b"disabled\0"
            report["disabled_optional"].append({"path": path,
                "reason": "combphy2 is physically wired to RTL8111 PCIe"})
    # Never retain the donor's selected HDMI/DP audio codec names.
    for path in ("/sound", "/sound-analog"):
        if path in result:
            result[path]["status"]=b"disabled\0"

    # Let Linux borrow the HDMI capture pool while it is idle. Keep the
    # donor's pool size and placement constraints for the HDMI RX driver.
    hdmi_cma = result["/reserved-memory/hdmi-receiver-cma"]
    if b"shared-dma-pool\0" not in hdmi_cma["compatible"]:
        raise RuntimeError("HDMI RX memory is not a shared DMA pool")
    hdmi_cma.pop("no-map", None)
    hdmi_cma["reusable"] = b""
    report["changes"].append({"path": "/reserved-memory/hdmi-receiver-cma",
        "property": "reusable"})

    # The factory firmware's memory banks exclude 0x08400000..0x09400000.
    # Chain-loaded mainline U-Boot rediscovers contiguous DRAM and otherwise
    # exposes this protected range to Linux. A 512 MiB tmpfs write then raises
    # an asynchronous SError at physical address 0x08400000. Keep the carveout
    # explicit so both the factory secure world and the new boot chain work.
    memory = next(v for v in factory.values() if v.get("device_type") == b"memory\0")
    banks = cells(memory["reg"])
    intervals = [((banks[i] << 32) | banks[i+1],
                  (banks[i+2] << 32) | banks[i+3]) for i in range(0, len(banks), 4)]
    assert any(start + size == 0x08400000 for start, size in intervals)
    assert any(start == 0x09400000 for start, size in intervals)
    result["/reserved-memory/secure-firmware@8400000"] = {
        "reg": packed([0, 0x08400000, 0, 0x01000000]), "no-map": b"",
    }
    report["changes"].append({"path": "/reserved-memory/secure-firmware@8400000",
        "property": "factory-secure-memory-hole"})

    # Reject dangling newly imported nodes: every imported pinconf has real content.
    placeholders = [
        p for p, v in result.items()
        if p not in base and set(v) <= {"phandle"}
        and not any(q.startswith(p + "/") for q in result)
    ]
    report["unresolved_nodes"] = placeholders
    if placeholders:
        (output_dir / "board-port-report.json").write_text(json.dumps(report, indent=2) + "\n")
        raise RuntimeError("Unresolved imported nodes: " + ", ".join(placeholders))
    lines = ["// SPDX-License-Identifier: (GPL-2.0+ OR MIT)", "/dts-v1/;", ""]
    lines.extend(emit_tree(result, "/"))
    output = output_dir / "rk3588-9tripod-I3588.dts"
    output.with_suffix(".expanded.dts").write_text("\n".join(lines) + "\n")
    base_source = output_dir / args.base_name
    base_source.write_text(
        "// SoC bindings and resource topology from the target kernel DTB.\n"
        + "\n".join(emit_tree(base, "/")) + "\n"
    )
    board_lines = [
        "// SPDX-License-Identifier: (GPL-2.0+ OR MIT)",
        "// Board wiring: original I3588 Linux+QT runtime DTB and BV2 carrier schematic.",
        "// Generated for the target kernel; validate individual peripherals.",
        "/dts-v1/;",
        f'/include/ "{args.base_name}"',
        "",
    ]
    # Keep inherited SoC resources in the base; emit only board-level differences.
    for path, properties in result.items():
        if path not in base:
            continue
        changed = {n: v for n, v in properties.items() if base[path].get(n) != v}
        deleted = [n for n in base[path] if n not in properties]
        added_children = [
            p for p in result if p not in base and (p.rsplit("/", 1)[0] or "/") == path
        ]
        deleted_children = [
            p for p in base if p not in result and (p.rsplit("/", 1)[0] or "/") == path
        ]
        if not changed and not deleted and not added_children and not deleted_children:
            continue
        board_lines.append(("/ {" if path == "/" else "&{" + path + "} {"))
        for name in deleted:
            board_lines.append("\t/delete-property/ " + name + ";")
        for name, value in changed.items():
            board_lines.append("\t" + format_prop(name, value))
        for child in deleted_children:
            board_lines.append("\t/delete-node/ " + child.rsplit("/", 1)[-1] + ";")
        for child in added_children:
            board_lines.extend(emit_tree(result, child, 1))
        board_lines.extend(["};", ""])
    # The target's exported symbol paths are not valid after board-specific deletion.
    # The generated source is intentionally a standalone overlay-style tree;
    # there is no symbol node to delete after flattening.
    output.write_text("\n".join(board_lines) + "\n")
    subprocess.run(["dtc", "-q", "-I", "dts", "-O", "dtb", "-o",
                    str(output.with_suffix(".dtb")), str(output)], check=True)
    assert read_tree(output.with_suffix(".dtb")) == result, "Source roundtrip changed properties"
    report["warning"] = "Development candidate: hardware validation pending."
    report["path_map"] = path_map
    (output_dir / "board-port-report.json").write_text(json.dumps(report, indent=2) + "\n")


if __name__ == "__main__":
    main()
