#!/bin/bash
set -euo pipefail
root=${1:?Usage: clean-fnos-rootfs.sh mounted-rootfs}
test -f "$root/etc/os-release" && test -d "$root/usr/trim"
test "$root" != / && mountpoint -q "$root"
here=$(cd "$(dirname "$0")" && pwd)
rm -f "$root/etc/ssh"/ssh_host_*
rm -f "$root/root/.bash_history" "$root/root/.ash_history" "$root/root/.viminfo" "$root/root/.lesshst"
rm -f "$root/root/.ssh/authorized_keys" "$root/root/.ssh/known_hosts"
rm -f "$root/var/lib/systemd/random-seed"
find "$root/var/log" -type f -exec truncate -s 0 {} +
# The donor image omits Exim's log directory. Keep the package's runtime
# directory and ownership while clearing log contents for a clean first boot.
if chroot "$root" getent passwd Debian-exim >/dev/null; then
    chroot "$root" install -d -o Debian-exim -g adm -m 2750 /var/log/exim4
fi
find "$root/var/cache/apt/archives" -maxdepth 1 -type f -name '*.deb' -delete
find "$root/var/lib/apt/lists" -maxdepth 1 -type f -delete
if [ -d "$root/var/lib/dhcp" ]; then
    find "$root/var/lib/dhcp" -maxdepth 1 -type f -name '*lease*' -delete
fi
if [ -d "$root/etc/NetworkManager/system-connections" ]; then
    find "$root/etc/NetworkManager/system-connections" -maxdepth 1 -type f -delete
fi
truncate -s 0 "$root/etc/machine-id"
rm -f "$root/var/lib/dbus/machine-id"
ln -s /etc/machine-id "$root/var/lib/dbus/machine-id"
printf '9tripod-I3588\n' > "$root/etc/hostname"
printf 'root:root\n' | chroot "$root" /usr/sbin/chpasswd
install -d -m 755 "$root/etc/ssh/sshd_config.d"
printf 'PermitRootLogin yes\nPasswordAuthentication yes\n' > "$root/etc/ssh/sshd_config.d/10-9tripod-I3588.conf"
install -D -m 0644 "$here/fnos-rootfs/etc/systemd/system/9tripod-I3588-ssh-hostkeys.service" "$root/etc/systemd/system/9tripod-I3588-ssh-hostkeys.service"
mkdir -p "$root/etc/systemd/system/multi-user.target.wants"
ln -sfn ../9tripod-I3588-ssh-hostkeys.service "$root/etc/systemd/system/multi-user.target.wants/9tripod-I3588-ssh-hostkeys.service"
ln -sfn /lib/systemd/system/ssh.service "$root/etc/systemd/system/multi-user.target.wants/ssh.service"
# I3588 uses the kernel GPIO fan/thermal binding, not the donor PWM channel.
ln -sfn /dev/null "$root/etc/systemd/system/pwm-fancontrol.service"
# Return freed Btrfs data/metadata extents to the image without touching the
# boot-critical gaps, loader, GPT or the other filesystems.
fstrim "$root" || true
