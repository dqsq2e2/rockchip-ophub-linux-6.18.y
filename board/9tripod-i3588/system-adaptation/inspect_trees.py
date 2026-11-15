"""Read DTB tokens without interpreting numeric cells as strings."""
import json
import struct
from pathlib import Path

HERE = Path(__file__).resolve().parent


def read_tree(path):
    data = Path(path).read_bytes()
    header = struct.unpack_from(">10I", data)
    assert header[0] == 0xD00DFEED
    strings = data[header[3]:header[3] + header[8]]
    pos = header[2]
    stack = []
    nodes = {}
    while True:
        token, = struct.unpack_from(">I", data, pos)
        pos += 4
        if token == 1:
            end = data.index(0, pos)
            stack.append(data[pos:end].decode())
            pos = (end + 4) & ~3
            nodes["/".join(stack) or "/"] = {}
        elif token == 2:
            stack.pop()
        elif token == 3:
            length, nameoff = struct.unpack_from(">II", data, pos)
            pos += 8
            end = strings.index(0, nameoff)
            name = strings[nameoff:end].decode()
            nodes["/".join(stack) or "/"][name] = data[pos:pos + length]
            pos = (pos + length + 3) & ~3
        elif token == 9:
            break
        elif token != 4:
            raise ValueError(token)
    return nodes


def text(value):
    return value.rstrip(b"\0").decode(errors="replace").replace("\0", ", ")


def inventory(nodes):
    handles = {
        struct.unpack(">I", props["phandle"])[0]: path
        for path, props in nodes.items() if "phandle" in props
    }
    result = []
    for path, props in nodes.items():
        if "/__symbols__" in path:
            continue
        if "compatible" not in props and "regulator-name" not in props:
            continue
        if text(props.get("status", b"okay")) == "disabled":
            continue
        item = {"path": path}
        for key in ("compatible", "status", "regulator-name", "phy-mode"):
            if key in props:
                item[key] = text(props[key])
        for key in ("gpio", "gpios", "reset-gpios", "pinctrl-0", "cpu-supply",
                    "mali-supply", "rknpu-supply", "vpcie3v3-supply",
                    "vmmc-supply", "vqmmc-supply", "mmc-pwrseq"):
            if key in props:
                cells = struct.unpack(">" + "I" * (len(props[key]) // 4), props[key])
                if key == "pinctrl-0":
                    item[key] = [handles.get(v, hex(v)) for v in cells]
                else:
                    item[key] = [handles.get(cells[0], hex(cells[0]))] + [
                        hex(v) for v in cells[1:]
                    ]
        result.append(item)
    return result


if __name__ == "__main__":
    boot = (HERE / "factory-boot.img").read_bytes()
    offset = 0
    candidates = []
    while True:
        offset = boot.find(b"\xd0\x0d\xfe\xed", offset)
        if offset < 0:
            break
        size = struct.unpack_from(">I", boot, offset + 4)[0]
        if 40 <= size <= 1024 * 1024 and offset + size <= len(boot):
            output = HERE / f"factory-boot-dtb-{offset:08x}.dtb"
            output.write_bytes(boot[offset:offset + size])
            try:
                tree = read_tree(output)
                candidates.append({"offset": offset, "size": size,
                                   "model": text(tree["/"].get("model", b""))})
            except (ValueError, AssertionError, IndexError):
                output.unlink()
        offset += 4
    (HERE / "factory-boot-dtbs.json").write_text(json.dumps(candidates, indent=2) + "\n")
    for source in ("factory-runtime.dtb", "rk3588-nanopc-t6.dtb"):
        tree = read_tree(HERE / source)
        dest = HERE / (source + ".inventory.json")
        dest.write_text(json.dumps(inventory(tree), indent=2) + "\n")
        print(dest)
