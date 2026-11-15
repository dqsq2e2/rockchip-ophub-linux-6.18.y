#!/usr/bin/env python3
"""Keep the distribution boot script valid with I3588 memory and mapped GPT."""
import argparse
from pathlib import Path
import re

p = argparse.ArgumentParser()
p.add_argument('boot', type=Path)
p.add_argument('--root-partuuid', required=True)
a = p.parse_args()
assert re.fullmatch(r'[0-9a-fA-F-]{36}', a.root_partuuid)
script = a.boot / 'boot.cmd'
s = script.read_text()
# 0x09000000 is inside the factory secure-world hole. Use a separate buffer
# below the kernel, away from the executing boot.scr at 0x00c00000.
s = s.replace('setenv load_addr "0x9000000"', 'setenv load_addr "0x01000000"')
needle = 'part uuid ${devtype} ${devnum}:${distro_rootpart} rootuuid'
replacement = '''# The outer recovery GPT changes partition numbers, but preserves PARTUUIDs.
# Resolve the root partition on the same medium as this boot script.
setenv i3588_root_found
part list ${devtype} ${devnum} i3588_root_parts
for i3588_root_part in ${i3588_root_parts}; do
    part uuid ${devtype} ${devnum}:${i3588_root_part} i3588_root_candidate
    if test "${i3588_root_candidate}" = "${i3588_rootuuid}"; then
        setenv distro_rootpart ${i3588_root_part}
        setenv i3588_root_found 1
    fi
done
if test "${i3588_root_found}" != "1"; then
    echo "I3588 root partition is missing on this boot medium"
    exit
fi
part uuid ${devtype} ${devnum}:${distro_rootpart} rootuuid'''
if '# Resolve the root partition on the same medium' not in s:
    assert s.count(needle) == 1
    s = s.replace(needle, replacement)
# update-initramfs produces a raw initramfs, requiring an explicit byte count.
s = s.replace('initrd_load_addr="${ramdisk_addr_r}"',
              'initrd_load_addr="${ramdisk_addr_r}:${filesize}"')
# Preserve the I3588 console baud rate of 1500000.
assert 'setenv load_addr "0x9000000"' not in s
assert 'initrd_load_addr="${ramdisk_addr_r}:${filesize}"' in s
script.write_text(s)
env = a.boot / 'fnEnv.txt'
lines = [line for line in env.read_text().splitlines() if not line.startswith('i3588_rootuuid=')]
lines.append('i3588_rootuuid=' + a.root_partuuid.lower())
env.write_text('\n'.join(lines) + '\n')
